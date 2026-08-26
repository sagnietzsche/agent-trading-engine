package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// migration steps, ported from the SeaORM migrations. Every statement is
// idempotent (IF NOT EXISTS) so databases created by the Rust backend boot
// cleanly too.
var migrationSteps = []string{
	// m20260822_000001_init
	`CREATE TABLE IF NOT EXISTS agents (
		id uuid PRIMARY KEY,
		name text NOT NULL,
		is_bot boolean NOT NULL DEFAULT false,
		cash numeric(20,4) NOT NULL DEFAULT 0,
		reserved_cash numeric(20,4) NOT NULL DEFAULT 0,
		created_at timestamptz NOT NULL DEFAULT now()
	)`,
	`CREATE TABLE IF NOT EXISTS stocks (
		symbol text PRIMARY KEY,
		name text NOT NULL,
		fair numeric(20,4) NOT NULL,
		prev_close numeric(20,4) NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS orders (
		id bigint PRIMARY KEY,
		agent_id uuid NOT NULL REFERENCES agents(id),
		symbol text NOT NULL REFERENCES stocks(symbol),
		side text NOT NULL,
		kind text NOT NULL,
		price numeric(20,4),
		qty integer NOT NULL,
		filled integer NOT NULL DEFAULT 0,
		status text NOT NULL,
		created_at timestamptz NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS idx_orders_agent ON orders(agent_id)`,
	`CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status)`,
	`CREATE TABLE IF NOT EXISTS trades (
		id uuid PRIMARY KEY,
		symbol text NOT NULL REFERENCES stocks(symbol),
		price numeric(20,4) NOT NULL,
		qty integer NOT NULL,
		buyer uuid NOT NULL,
		seller uuid NOT NULL,
		taker_order bigint NOT NULL REFERENCES orders(id),
		buyer_equity numeric(20,4) NOT NULL DEFAULT 0,
		seller_equity numeric(20,4) NOT NULL DEFAULT 0,
		gini_after numeric(10,6) NOT NULL DEFAULT 0,
		ts timestamptz NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS idx_trades_symbol_ts ON trades(symbol, ts)`,
	`CREATE TABLE IF NOT EXISTS positions (
		agent_id uuid NOT NULL REFERENCES agents(id),
		symbol text NOT NULL REFERENCES stocks(symbol),
		qty integer NOT NULL DEFAULT 0,
		PRIMARY KEY (agent_id, symbol)
	)`,
	`CREATE TABLE IF NOT EXISTS welfare_snapshots (
		id bigserial PRIMARY KEY,
		gini numeric(10,6) NOT NULL,
		total_equity numeric(22,4) NOT NULL,
		mean_equity numeric(20,4) NOT NULL,
		ts timestamptz NOT NULL DEFAULT now()
	)`,
	// m20260822_000002_tournaments
	`CREATE TABLE IF NOT EXISTS tournaments (
		id uuid PRIMARY KEY,
		name text NOT NULL,
		status text NOT NULL DEFAULT 'open',
		duration_ticks integer NOT NULL DEFAULT 90,
		ticks_left integer NOT NULL DEFAULT 90,
		gini_start numeric(10,6) NOT NULL DEFAULT 0,
		gini_final numeric(10,6),
		created_at timestamptz NOT NULL DEFAULT now(),
		started_at timestamptz,
		finished_at timestamptz
	)`,
	`CREATE TABLE IF NOT EXISTS tournament_entries (
		tournament_id uuid NOT NULL REFERENCES tournaments(id) ON DELETE CASCADE,
		agent_id uuid NOT NULL REFERENCES agents(id),
		strategy text NOT NULL DEFAULT 'custom',
		start_equity numeric(20,4) NOT NULL DEFAULT 0,
		total_volume bigint NOT NULL DEFAULT 0,
		prosocial_volume bigint NOT NULL DEFAULT 0,
		return_pct numeric(14,6),
		coop_share numeric(10,6),
		score numeric(16,6),
		finished_at timestamptz,
		PRIMARY KEY (tournament_id, agent_id)
	)`,
}

// migrate applies pending migration steps in order, tracking applied versions
// in schema_migrations (compatible with any pre-existing database).
func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version integer PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	var applied int
	if err := pool.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&applied); err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	for i, sql := range migrationSteps {
		version := i + 1
		if version <= applied {
			continue
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, sql); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %d: %w", version, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		logInfo("applied migration %d", version)
	}
	return nil
}
