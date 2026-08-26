package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

const simInterval = time.Second

type pgxPool = pgxpool.Pool

// lockedExchange guards the matching engine. Writers (order placement, sim
// ticks) take the exclusive lock; read-model builders (REST reads, WS frame
// assembly) take the shared lock so many clients can read concurrently.
type lockedExchange struct {
	mu sync.RWMutex
	ex *Exchange
}

func (l *lockedExchange) lock() *Exchange {
	l.mu.Lock()
	return l.ex
}

func (l *lockedExchange) unlock() {
	l.mu.Unlock()
}

func (l *lockedExchange) rlock() *Exchange {
	l.mu.RLock()
	return l.ex
}

func (l *lockedExchange) runlock() {
	l.mu.RUnlock()
}

// logInfo is a tiny structured logger shim used across the package.
func logInfo(format string, args ...any) {
	slog.Info(fmt.Sprintf(format, args...))
}

func main() {
	_ = godotenv.Load() // optional .env, like dotenvy in the Rust backend

	setupLogging()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := connectDB(ctx)
	if err != nil {
		slog.Error("failed to connect to Postgres", "err", err)
		slog.Error("start the database first:  docker compose up -d")
		os.Exit(1)
	}
	defer db.Close()

	if err := migrate(ctx, db); err != nil {
		slog.Error("failed to run migrations", "err", err)
		os.Exit(1)
	}

	empty, err := isDBEmpty(ctx, db)
	if err != nil {
		slog.Error("inspect database", "err", err)
		os.Exit(1)
	}
	if empty {
		slog.Info("empty database: seeding listings and system agents")
		if err := seedFresh(ctx, db); err != nil {
			slog.Error("seed database", "err", err)
			os.Exit(1)
		}
		ex := FreshSimulated()
		pending := ex.DrainPending()
		if err := flush(ctx, db, &pending); err != nil {
			slog.Error("persist opening state", "err", err)
			os.Exit(1)
		}
	}

	exchange, err := bootExchange(ctx, db)
	if err != nil {
		slog.Error("failed to rebuild exchange from Postgres", "err", err)
		os.Exit(1)
	}
	slog.Info("restored %d listings, %d agents from postgres", "listings", len(exchange.Symbols), "agents", len(exchange.Agents))

	srv := &server{
		ctx:     ctx,
		db:      db,
		ex:      &lockedExchange{ex: exchange},
		flusher: newFlusher(512),
	}
	srv.flusher.start(db)

	// Market simulation: random-walk fairs, neutral MM quotes, solidarity flow.
	// Writes are batched to the background flusher so the tick never blocks on
	// Postgres.
	go func() {
		ticker := time.NewTicker(simInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			ex := srv.ex.lock()
			ex.SimTick()
			pending := ex.DrainPending()
			srv.ex.unlock()
			srv.submitFlush(&pending)
		}
	}()

	host := os.Getenv("HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := host + ":" + port

	srv.ex.rlock()
	nSymbols := len(srv.ex.ex.Symbols)
	nAgents := len(srv.ex.ex.Agents)
	srv.ex.runlock()

	slog.Info("trading engine listening on http://%s (%d listings, %d agents)", "addr", addr, "listings", nSymbols, "agents", nAgents)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		// Drain whatever engine writes are still queued before the pool closes.
		srv.flusher.stop(15 * time.Second)
	}()

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server failed", "err", err)
		os.Exit(1)
	}
	<-shutdownDone
}

func setupLogging() {
	level := slog.LevelInfo
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	case "trace":
		level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}
