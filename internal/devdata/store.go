package devdata

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/fake"
)

// migrationTable is never touched by Reset.
const migrationTable = "goose_db_version"

// ApplyRequest configures a scenario run.
type ApplyRequest struct {
	Reset  bool              `json:"reset"`  // wipe all data first
	Seed   uint64            `json:"seed"`   // 0 = random; the used seed is returned for reproducibility
	Params map[string]string `json:"params"` // raw values, validated against the scenario
}

// Result describes a completed scenario run.
type Result struct {
	Scenario string        `json:"scenario"`
	Seed     uint64        `json:"seed"`
	Reset    bool          `json:"reset"`
	Summary  string        `json:"summary"`
	Duration time.Duration `json:"durationNs"`
}

// TableStat is the row count of one table.
type TableStat struct {
	Table string `json:"table"`
	Rows  int64  `json:"rows"`
}

// Store runs scenarios and maintenance operations against the database.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store using pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Apply runs a scenario inside a single transaction.
func (s *Store) Apply(ctx context.Context, name string, req ApplyRequest) (Result, error) {
	sc, err := Find(name)
	if err != nil {
		return Result{}, err
	}
	vals, err := sc.Resolve(req.Params)
	if err != nil {
		return Result{}, err
	}
	seed := req.Seed
	if seed == 0 {
		seed = uint64(time.Now().UnixNano())
	}
	reset := req.Reset || sc.AlwaysReset
	start := time.Now()

	var summary string
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if reset {
			if err := truncateAll(ctx, tx); err != nil {
				return err
			}
		}
		var err error
		summary, err = sc.Run(ctx, Env{Q: sqlc.New(tx), Fake: fake.New(seed)}, vals)
		return err
	})
	if err != nil {
		return Result{}, fmt.Errorf("devdata: scenario %s: %w", name, err)
	}
	return Result{Scenario: name, Seed: seed, Reset: reset, Summary: summary, Duration: time.Since(start)}, nil
}

// Reset truncates every application table.
func (s *Store) Reset(ctx context.Context) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error { return truncateAll(ctx, tx) })
}

// Stats returns row counts for every application table.
func (s *Store) Stats(ctx context.Context) ([]TableStat, error) {
	tables, err := appTables(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	stats := make([]TableStat, 0, len(tables))
	for _, t := range tables {
		var n int64
		if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM "+quote(t)).Scan(&n); err != nil {
			return nil, fmt.Errorf("devdata: count %s: %w", t, err)
		}
		stats = append(stats, TableStat{Table: t, Rows: n})
	}
	return stats, nil
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// appTables lists public tables, excluding goose's table.
// It is schema-driven, so new tables are picked up automatically.
func appTables(ctx context.Context, q querier) ([]string, error) {
	rows, err := q.Query(ctx,
		`SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename <> $1 ORDER BY tablename`,
		migrationTable)
	if err != nil {
		return nil, fmt.Errorf("devdata: list tables: %w", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("devdata: list tables: %w", err)
	}
	return names, nil
}

func quote(table string) string { return pgx.Identifier{table}.Sanitize() }

func truncateAll(ctx context.Context, tx pgx.Tx) error {
	tables, err := appTables(ctx, tx)
	if err != nil || len(tables) == 0 {
		return err
	}
	quoted := make([]string, len(tables))
	for i, t := range tables {
		quoted[i] = quote(t)
	}
	if _, err := tx.Exec(ctx, "TRUNCATE "+strings.Join(quoted, ", ")+" RESTART IDENTITY CASCADE"); err != nil {
		return fmt.Errorf("devdata: truncate: %w", err)
	}
	return nil
}
