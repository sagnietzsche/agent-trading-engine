package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func connectDB(ctx context.Context) (*pgxpool.Pool, error) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return nil, errors.New("DATABASE_URL must be set (.env supported)")
	}
	return pgxpool.New(ctx, url)
}

// IsEmpty reports whether no agents exist yet (fresh database).
func isDBEmpty(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var n int64
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM agents`).Scan(&n)
	return n == 0, err
}

// ResetAll wipes every table. Used by POST /api/admin/reset before a fresh reseed.
func resetAll(ctx context.Context, pool *pgxpool.Pool) error {
	for _, sql := range []string{
		`DELETE FROM tournament_entries`,
		`DELETE FROM tournaments`,
		`DELETE FROM trades`,
		`DELETE FROM orders`,
		`DELETE FROM positions`,
		`DELETE FROM welfare_snapshots`,
		`DELETE FROM agents`,
		`DELETE FROM stocks`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			return err
		}
	}
	return nil
}

// SeedFresh inserts listings + system agents. Only called when the DB starts empty.
func seedFresh(ctx context.Context, pool *pgxpool.Pool) error {
	for _, l := range Listings {
		symbol := l[0].(string)
		name := l[1].(string)
		base := l[2].(float64)
		_, err := pool.Exec(ctx, `
			INSERT INTO stocks (symbol, name, fair, prev_close)
			VALUES ($1, $2, $3, $3)
			ON CONFLICT (symbol) DO NOTHING`, symbol, name, base)
		if err != nil {
			return err
		}
	}

	bots := []struct {
		id   uuid.UUID
		name string
		cash float64
		inv  int
	}{
		{MarketMakerID, "market_maker", 10_000_000.0, 0},
		{SolidarityID, liquidityBotName(marketRegimeFromEnv()), 6_000_000.0, 40_000},
	}
	for _, b := range bots {
		_, err := pool.Exec(ctx, `
			INSERT INTO agents (id, name, is_bot, cash, reserved_cash)
			VALUES ($1, $2, true, $3, 0)
			ON CONFLICT (id) DO NOTHING`, b.id, b.name, b.cash)
		if err != nil {
			return err
		}
		if b.inv == 0 {
			continue
		}
		for _, l := range Listings {
			symbol := l[0].(string)
			_, err := pool.Exec(ctx, `
				INSERT INTO positions (agent_id, symbol, qty)
				VALUES ($1, $2, $3)
				ON CONFLICT (agent_id, symbol) DO UPDATE SET qty = EXCLUDED.qty`,
				b.id, symbol, b.inv)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// loadedRows mirrors what boot rebuild needs from Postgres.
type loadedRows struct {
	agents     []agentRow
	stocks     []stockRow
	positions  []positionRow
	openOrders []orderRow
	maxOrderID int64
	lastTrades map[string]float64
}

type agentRow struct {
	ID           uuid.UUID
	Name         string
	IsBot        bool
	Cash         float64
	ReservedCash float64
}

type stockRow struct {
	Symbol    string
	Name      string
	Fair      float64
	PrevClose float64
}

type positionRow struct {
	AgentID uuid.UUID
	Symbol  string
	Qty     int64
}

type orderRow struct {
	ID        int64
	AgentID   uuid.UUID
	Symbol    string
	Side      string
	Kind      string
	Price     *float64
	Qty       int32
	Filled    int32
	Status    string
	CreatedAt time.Time
}

func loadRows(ctx context.Context, pool *pgxpool.Pool) (loadedRows, error) {
	var out loadedRows
	out.lastTrades = map[string]float64{}

	rows, err := pool.Query(ctx, `SELECT id, name, is_bot, cash::float8, reserved_cash::float8 FROM agents`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r agentRow
		if err := rows.Scan(&r.ID, &r.Name, &r.IsBot, &r.Cash, &r.ReservedCash); err != nil {
			rows.Close()
			return out, err
		}
		out.agents = append(out.agents, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	rows, err = pool.Query(ctx, `SELECT symbol, name, fair::float8, prev_close::float8 FROM stocks`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r stockRow
		if err := rows.Scan(&r.Symbol, &r.Name, &r.Fair, &r.PrevClose); err != nil {
			rows.Close()
			return out, err
		}
		out.stocks = append(out.stocks, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	rows, err = pool.Query(ctx, `SELECT agent_id, symbol, qty FROM positions`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r positionRow
		if err := rows.Scan(&r.AgentID, &r.Symbol, &r.Qty); err != nil {
			rows.Close()
			return out, err
		}
		out.positions = append(out.positions, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	rows, err = pool.Query(ctx, `
		SELECT id, agent_id, symbol, side, kind, price::float8, qty, filled, status, created_at
		FROM orders WHERE status IN ('open', 'partially_filled')`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r orderRow
		if err := rows.Scan(&r.ID, &r.AgentID, &r.Symbol, &r.Side, &r.Kind, &r.Price, &r.Qty, &r.Filled, &r.Status, &r.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.openOrders = append(out.openOrders, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	if err := pool.QueryRow(ctx, `SELECT COALESCE(MAX(id), 0) FROM orders`).Scan(&out.maxOrderID); err != nil {
		return out, err
	}

	rows, err = pool.Query(ctx, `SELECT symbol, price::float8 FROM trades ORDER BY ts DESC LIMIT 1000`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var sym string
		var price float64
		if err := rows.Scan(&sym, &price); err != nil {
			rows.Close()
			return out, err
		}
		if _, ok := out.lastTrades[sym]; !ok {
			out.lastTrades[sym] = price
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	return out, nil
}

func toRestore(rows loadedRows) RestoreState {
	agents := map[uuid.UUID]AgentCache{}
	for _, a := range rows.agents {
		agents[a.ID] = AgentCache{
			ID:             a.ID,
			Name:           a.Name,
			IsBot:          a.IsBot,
			Cash:           a.Cash,
			ReservedCash:   0.0, // recomputed from open orders by Restore
			Positions:      map[string]int64{},
			ReservedShares: map[string]uint32{},
		}
	}
	for _, p := range rows.positions {
		if c, ok := agents[p.AgentID]; ok {
			c.Positions[p.Symbol] = p.Qty
			agents[p.AgentID] = c
		}
	}

	stocks := make([]StockInfo, 0, len(rows.stocks))
	for _, s := range rows.stocks {
		var last *float64
		if lt, ok := rows.lastTrades[s.Symbol]; ok {
			last = &lt
		}
		stocks = append(stocks, StockInfo{
			Symbol:    s.Symbol,
			Name:      s.Name,
			Fair:      s.Fair,
			LastTrade: last,
			PrevClose: s.PrevClose,
		})
	}

	opens := make([]OrderRecord, 0, len(rows.openOrders))
	for _, o := range rows.openOrders {
		side := SideBuy
		if o.Side == "sell" {
			side = SideSell
		}
		kind := KindLimit
		if o.Kind == "market" {
			kind = KindMarket
		}
		qty := o.Qty
		if qty < 0 {
			qty = 0
		}
		filled := o.Filled
		if filled < 0 {
			filled = 0
		}
		if filled > qty {
			filled = qty
		}
		opens = append(opens, OrderRecord{
			ID:        uint64(o.ID),
			AgentID:   o.AgentID,
			Symbol:    o.Symbol,
			Side:      side,
			Kind:      kind,
			Price:     o.Price,
			Qty:       uint32(qty),
			Filled:    uint32(filled),
			Status:    StatusOpen,
			CreatedAt: o.CreatedAt.Format(time.RFC3339Nano),
		})
	}

	return RestoreState{
		Stocks:      stocks,
		Agents:      agentValues(agents),
		OpenOrders:  opens,
		NextOrderID: uint64(rows.maxOrderID) + 1,
	}
}

func agentValues(m map[uuid.UUID]AgentCache) []AgentCache {
	out := make([]AgentCache, 0, len(m))
	for _, c := range m {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out
}

// bootExchange builds the in-memory exchange from whatever is in Postgres,
// using the process's WELFARE_METRIC and MARKET_REGIME env vars (both are
// instance config, not persisted state).
func bootExchange(ctx context.Context, pool *pgxpool.Pool) (*Exchange, error) {
	rows, err := loadRows(ctx, pool)
	if err != nil {
		return nil, err
	}
	state := toRestore(rows)
	state.Tournaments, err = loadOpenTournaments(ctx, pool)
	if err != nil {
		return nil, err
	}
	state.Metric = welfareMetricFromEnv()
	state.Regime = marketRegimeFromEnv()
	return Restore(state), nil
}

// loadOpenTournaments reloads unfinished tournaments so a restart doesn't
// kill a live competition.
func loadOpenTournaments(ctx context.Context, pool *pgxpool.Pool) ([]Tournament, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, name, status, duration_ticks, ticks_left, gini_start::float8, gini_final::float8,
		       created_at, started_at, finished_at
		FROM tournaments WHERE status IN ('open', 'running')`)
	if err != nil {
		return nil, err
	}
	type tRow struct {
		id            uuid.UUID
		name          string
		status        string
		durationTicks int32
		ticksLeft     int32
		giniStart     float64
		giniFinal     *float64
		createdAt     time.Time
		startedAt     *time.Time
		finishedAt    *time.Time
	}
	var ts []tRow
	for rows.Next() {
		var r tRow
		if err := rows.Scan(&r.id, &r.name, &r.status, &r.durationTicks, &r.ticksLeft, &r.giniStart, &r.giniFinal, &r.createdAt, &r.startedAt, &r.finishedAt); err != nil {
			rows.Close()
			return nil, err
		}
		ts = append(ts, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ts) == 0 {
		return nil, nil
	}

	ids := make([]uuid.UUID, len(ts))
	for i, r := range ts {
		ids[i] = r.id
	}
	erows, err := pool.Query(ctx, `
		SELECT tournament_id, agent_id, strategy, start_equity::float8, total_volume, prosocial_volume
		FROM tournament_entries WHERE tournament_id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	type eRow struct {
		tournamentID    uuid.UUID
		agentID         uuid.UUID
		strategy        string
		startEquity     float64
		totalVolume     int64
		prosocialVolume int64
	}
	var ers []eRow
	for erows.Next() {
		var r eRow
		if err := erows.Scan(&r.tournamentID, &r.agentID, &r.strategy, &r.startEquity, &r.totalVolume, &r.prosocialVolume); err != nil {
			erows.Close()
			return nil, err
		}
		ers = append(ers, r)
	}
	erows.Close()
	if err := erows.Err(); err != nil {
		return nil, err
	}

	out := make([]Tournament, 0, len(ts))
	for _, r := range ts {
		entries := map[uuid.UUID]TournamentEntry{}
		for _, e := range ers {
			if e.tournamentID != r.id {
				continue
			}
			tv := e.totalVolume
			if tv < 0 {
				tv = 0
			}
			pv := e.prosocialVolume
			if pv < 0 {
				pv = 0
			}
			entries[e.agentID] = TournamentEntry{
				AgentID:         e.agentID,
				Strategy:        e.strategy,
				StartEquity:     e.startEquity,
				TotalVolume:     uint64(tv),
				ProsocialVolume: uint64(pv),
			}
		}
		status := TStatusOpen
		if r.status == "running" {
			status = TStatusRunning
		}
		duration := r.durationTicks
		if duration < 1 {
			duration = 1
		}
		ticksLeft := r.ticksLeft
		if ticksLeft < 0 {
			ticksLeft = 0
		}
		if ticksLeft > duration {
			ticksLeft = duration
		}
		var startedAt, finishedAt *string
		if r.startedAt != nil {
			s := r.startedAt.UTC().Format(time.RFC3339Nano)
			startedAt = &s
		}
		if r.finishedAt != nil {
			s := r.finishedAt.UTC().Format(time.RFC3339Nano)
			finishedAt = &s
		}
		out = append(out, Tournament{
			ID:            r.id,
			Name:          r.name,
			Status:        status,
			DurationTicks: uint32(duration),
			TicksLeft:     uint32(ticksLeft),
			GiniStart:     r.giniStart,
			GiniFinal:     r.giniFinal,
			Entries:       entries,
			CreatedAt:     r.createdAt.UTC().Format(time.RFC3339Nano),
			StartedAt:     startedAt,
			FinishedAt:    finishedAt,
		})
	}
	return out, nil
}

// saveTournament upserts a tournament snapshot plus all of its entries.
func saveTournament(ctx context.Context, q pgxQuerier, view *TournamentView) error {
	var giniFinal *float64
	var startedAt, finishedAt *time.Time
	if view.GiniFinal != nil {
		g := *view.GiniFinal
		giniFinal = &g
	}
	if view.StartedAt != nil {
		t, err := parseTS(*view.StartedAt)
		if err != nil {
			return err
		}
		startedAt = &t
	}
	if view.FinishedAt != nil {
		t, err := parseTS(*view.FinishedAt)
		if err != nil {
			return err
		}
		finishedAt = &t
	}
	_, err := q.Exec(ctx, `
		INSERT INTO tournaments (id, name, status, duration_ticks, ticks_left, gini_start, gini_final, created_at, started_at, finished_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			status = EXCLUDED.status,
			duration_ticks = EXCLUDED.duration_ticks,
			ticks_left = EXCLUDED.ticks_left,
			gini_start = EXCLUDED.gini_start,
			gini_final = EXCLUDED.gini_final,
			started_at = EXCLUDED.started_at,
			finished_at = EXCLUDED.finished_at`,
		view.ID, view.Name, string(view.Status), int32(view.DurationTicks), int32(view.TicksLeft),
		view.GiniStart, giniFinal, mustParseTS(view.CreatedAt), startedAt, finishedAt)
	if err != nil {
		return err
	}
	for _, e := range view.Entries {
		_, err := q.Exec(ctx, `
			INSERT INTO tournament_entries (tournament_id, agent_id, strategy, start_equity, total_volume, prosocial_volume, return_pct, coop_share, score, finished_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (tournament_id, agent_id) DO UPDATE SET
				strategy = EXCLUDED.strategy,
				start_equity = EXCLUDED.start_equity,
				total_volume = EXCLUDED.total_volume,
				prosocial_volume = EXCLUDED.prosocial_volume,
				return_pct = EXCLUDED.return_pct,
				coop_share = EXCLUDED.coop_share,
				score = EXCLUDED.score,
				finished_at = EXCLUDED.finished_at`,
			view.ID, e.AgentID, e.Strategy, e.StartEquity, int64(e.TotalVolume), int64(e.ProsocialVolume),
			e.ReturnPct, e.CoopShare, e.Score, finishedAt)
		if err != nil {
			return err
		}
	}
	return nil
}

// pgxQuerier abstracts *pgxpool.Pool and pgx.Tx for saveTournament.
type pgxQuerier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func parseTS(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
	}
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

func mustParseTS(s string) time.Time {
	t, err := parseTS(s)
	if err != nil {
		return time.Now().UTC()
	}
	return t
}

// FlushTimeout bounds each write batch; a slow database degrades the writer
// instead of the request path.
const flushTimeout = 10 * time.Second

// ---- async writer -----------------------------------------------------------

// flushJob is one unit of work for the background writer. A non-nil done
// channel turns the job into a barrier used by drain to wait until every
// previously submitted job has been persisted.
type flushJob struct {
	pending *Pending
	done    chan struct{}
}

// flusher serializes all engine writes onto a single background goroutine so
// request handlers never pay a synchronous Postgres round-trip. Jobs are
// processed FIFO, which preserves the order the engine drained them in.
type flusher struct {
	mu     sync.Mutex
	ch     chan flushJob
	closed bool
	done   chan struct{}
}

func newFlusher(buf int) *flusher {
	return &flusher{ch: make(chan flushJob, buf), done: make(chan struct{})}
}

func (f *flusher) start(db *pgxpool.Pool) {
	go func() {
		defer close(f.done)
		for job := range f.ch {
			fctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
			err := flush(fctx, db, job.pending)
			cancel()
			if err != nil {
				slog.Error("async flush failed", "err", err)
			}
			if job.done != nil {
				close(job.done)
			}
		}
	}()
}

// submit queues a drained pending batch for persistence. It blocks when the
// writer is saturated (backpressure) and returns false once the flusher has
// been stopped.
func (f *flusher) submit(pending *Pending) bool {
	if pending.isEmpty() {
		return true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return false
	}
	f.ch <- flushJob{pending: pending}
	return true
}

// drain blocks until every job submitted before the call has been persisted.
// Used by /api/admin/reset so a stale queued batch can't resurrect rows after
// the tables are wiped.
func (f *flusher) drain(ctx context.Context) bool {
	done := make(chan struct{})
	p := newPending()
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return true
	}
	f.ch <- flushJob{pending: &p, done: done}
	f.mu.Unlock()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

// stop closes the queue and waits (bounded) for the writer to drain whatever
// is left before the database pool closes.
func (f *flusher) stop(timeout time.Duration) {
	f.mu.Lock()
	if !f.closed {
		f.closed = true
		close(f.ch)
	}
	f.mu.Unlock()
	select {
	case <-f.done:
	case <-time.After(timeout):
		slog.Warn("flusher did not drain in time")
	}
}

// Flush is the write-through persistence for everything the engine mutated in
// one step, applied in a single transaction.
func flush(ctx context.Context, pool *pgxpool.Pool, pending *Pending) error {
	if pending.isEmpty() {
		return nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, c := range pending.Agents {
		_, err := tx.Exec(ctx, `
			INSERT INTO agents (id, name, is_bot, cash, reserved_cash, created_at)
			VALUES ($1, $2, $3, $4, $5, now())
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				is_bot = EXCLUDED.is_bot,
				cash = EXCLUDED.cash,
				reserved_cash = EXCLUDED.reserved_cash`,
			c.ID, c.Name, c.IsBot, c.Cash, c.ReservedCash)
		if err != nil {
			return err
		}
	}

	for key, qty := range pending.Positions {
		_, err := tx.Exec(ctx, `
			INSERT INTO positions (agent_id, symbol, qty)
			VALUES ($1, $2, $3)
			ON CONFLICT (agent_id, symbol) DO UPDATE SET qty = EXCLUDED.qty`,
			key.AgentID, key.Symbol, int32(qty))
		if err != nil {
			return err
		}
	}

	for _, rec := range pending.Orders {
		var price *float64
		if rec.Price != nil {
			p := *rec.Price
			price = &p
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (id, agent_id, symbol, side, kind, price, qty, filled, status, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (id) DO UPDATE SET
				filled = EXCLUDED.filled,
				status = EXCLUDED.status,
				price = EXCLUDED.price`,
			int64(rec.ID), rec.AgentID, rec.Symbol, string(rec.Side), string(rec.Kind),
			price, int32(rec.Qty), int32(rec.Filled), string(rec.Status), mustParseTS(rec.CreatedAt))
		if err != nil {
			return err
		}
	}

	for _, t := range pending.Trades {
		tradeID, err := uuid.Parse(t.ID)
		if err != nil {
			tradeID = uuid.New()
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO trades (id, symbol, price, qty, buyer, seller, taker_order, buyer_equity, seller_equity, gini_after, ts)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (id) DO NOTHING`,
			tradeID, t.Symbol, t.Price, int32(t.Qty), t.Buyer, t.Seller,
			int64(t.TakerOrder), t.BuyerEquity, t.SellerEquity, t.GiniAfter, mustParseTS(t.TS))
		if err != nil {
			return err
		}
	}

	for _, s := range pending.Snapshots {
		_, err := tx.Exec(ctx, `
			INSERT INTO welfare_snapshots (gini, metric, metric_value, total_equity, mean_equity, ts)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			s.Gini, string(s.Metric), s.MetricValue, s.TotalEquity, s.MeanEquity, mustParseTS(s.TS))
		if err != nil {
			return err
		}
	}
	for i := range pending.TournamentsFinalized {
		if err := saveTournament(ctx, tx, &pending.TournamentsFinalized[i]); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// WelfarePoint is one sample of the recent welfare trend.
type WelfarePoint struct {
	Gini        float64 `json:"gini"`
	Metric      string  `json:"metric"`
	MetricValue float64 `json:"metric_value"`
	TotalEquity float64 `json:"total_equity"`
	MeanEquity  float64 `json:"mean_equity"`
	TS          string  `json:"ts"`
}

// WelfareHistory returns the recent welfare trend, newest last, for charting.
func welfareHistory(ctx context.Context, pool *pgxpool.Pool, limit int) ([]WelfarePoint, error) {
	rows, err := pool.Query(ctx, `
		SELECT gini::float8, metric, COALESCE(metric_value::float8, gini::float8), total_equity::float8, mean_equity::float8, ts
		FROM welfare_snapshots ORDER BY ts ASC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WelfarePoint
	for rows.Next() {
		var p WelfarePoint
		var ts time.Time
		if err := rows.Scan(&p.Gini, &p.Metric, &p.MetricValue, &p.TotalEquity, &p.MeanEquity, &ts); err != nil {
			return nil, err
		}
		p.TS = ts.UTC().Format(time.RFC3339Nano)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Keep only the most recent `limit` points in chronological order.
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}
