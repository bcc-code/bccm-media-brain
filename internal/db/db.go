// Package db holds database connectivity, embedded migrations and sqlc-generated queries.
package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// DefaultMaxConns is the pool size when DATABASE_URL does not set pool_max_conns. pgx's own
	// default is the CPU count, at least 4: one GraphQL request runs several statements at
	// once, so on a small container a single deep query filled the pool.
	DefaultMaxConns = 16

	// StatementTimeout stops a statement the request's deadline did not: the sweeper and the
	// CLI have no deadline of their own. It is above the 30 s a /query request may run, so a
	// request still ends by its deadline, with TIMEOUT, and not by this.
	StatementTimeout = 35 * time.Second

	// IdleInTransactionTimeout ends a session that holds a transaction open, and with it row
	// locks, without running anything: no code path waits inside a transaction.
	IdleInTransactionTimeout = time.Minute
)

// poolConfig is DATABASE_URL with the defaults above for what it leaves unsaid. A value in the
// URL wins: pool_max_conns, statement_timeout, idle_in_transaction_session_timeout.
func poolConfig(url string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	// ParseConfig fills in pgx's default, so the URL is the only place that says whether the
	// size was chosen.
	if !strings.Contains(url, "pool_max_conns") {
		cfg.MaxConns = DefaultMaxConns
	}
	for name, d := range map[string]time.Duration{
		"statement_timeout":                   StatementTimeout,
		"idle_in_transaction_session_timeout": IdleInTransactionTimeout,
	} {
		if _, set := cfg.ConnConfig.RuntimeParams[name]; !set {
			cfg.ConnConfig.RuntimeParams[name] = fmt.Sprint(d.Milliseconds())
		}
	}
	return cfg, nil
}

// Connect opens a pgx connection pool and verifies it with a ping.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := poolConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: open pool: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}
