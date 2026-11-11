package devdata

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/internal/fake"
	"github.com/bcc-media/mediabrain/internal/vidispineimport"
)

// migrationTable is never touched by Reset.
const migrationTable = "goose_db_version"

// referenceTables hold lookup data seeded by migrations (asset types, languages). Reset keeps
// them, because no scenario recreates them. Add a table here when a migration seeds one.
// They must not reference application tables, or TRUNCATE ... CASCADE would still empty them.
var referenceTables = []string{"asset_type", "language"}

func isReference(table string) bool {
	for _, t := range referenceTables {
		if t == table {
			return true
		}
	}
	return false
}

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
	Table     string `json:"table"`
	Rows      int64  `json:"rows"`
	Reference bool   `json:"reference,omitempty"` // seeded lookup data, kept by Reset
}

// Store runs scenarios and maintenance operations against the database.
type Store struct {
	pool *pgxpool.Pool

	// Vidispine is where the debug UI imports from; nil when Vidispine is not configured.
	Vidispine vidispineimport.Source
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
	ctx = access.With(ctx, access.System())

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

// User is one row of the debug UI's user switcher.
type User struct {
	ID     string  `json:"id"`
	Email  *string `json:"email"`
	Name   *string `json:"name"`
	Admin  bool    `json:"admin"`
	Active bool    `json:"active"`
	// Service marks a machine client.
	Service bool `json:"service"`
}

func userRow(u sqlc.AppUser) User {
	return User{ID: u.ID, Email: u.Email, Name: u.Name, Admin: u.IsAdmin, Active: u.Active, Service: u.Kind == sqlc.UserKindService}
}

func (s *Store) accessService() *access.Service {
	return access.NewService(sqlc.New(s.pool), db.PoolTx(s.pool))
}

// Users lists the users one can act as from the debug UI.
func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.accessService().Users(access.With(ctx, access.System()), domain.MaxLimit, 0)
	if err != nil {
		return nil, err
	}
	out := make([]User, len(rows))
	for i, u := range rows {
		out[i] = userRow(u)
	}
	return out, nil
}

// CreateUser adds a user to act as, without going through the identity provider.
func (s *Store) CreateUser(ctx context.Context, email string, admin bool) (User, error) {
	email, err := domain.Required("email", email)
	if err != nil {
		return User{}, err
	}
	u, err := s.accessService().CreateUser(access.With(ctx, access.System()), access.UserInput{
		ExternalID: "debug|" + email, Email: email, Name: email, Admin: admin,
	})
	return userRow(u), err
}

// VidispineAsset is what the debug UI shows after an import or a refresh.
type VidispineAsset struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	LegacyIDs []string        `json:"legacyIds"`
	Metadata  json.RawMessage `json:"metadata"`
}

func vidispineAsset(a sqlc.Asset, err error) (VidispineAsset, error) {
	return VidispineAsset{ID: a.ID, Title: a.Title, LegacyIDs: a.LegacyIds, Metadata: a.Metadata}, err
}

func (s *Store) vidispineService() *vidispineimport.Service {
	return vidispineimport.NewService(sqlc.New(s.pool), db.PoolTx(s.pool), s.Vidispine)
}

func (s *Store) VidispineConfigured() bool { return s.Vidispine != nil }

// VidispineImport imports one item from the configured Vidispine, as the system principal.
func (s *Store) VidispineImport(ctx context.Context, in vidispineimport.ImportInput) (VidispineAsset, error) {
	return vidispineAsset(s.vidispineService().Import(access.With(ctx, access.System()), in))
}

// VidispineRefresh refreshes the Vidispine metadata of an imported asset.
func (s *Store) VidispineRefresh(ctx context.Context, assetID string) (VidispineAsset, error) {
	return vidispineAsset(s.vidispineService().Refresh(access.With(ctx, access.System()), assetID))
}

// Reset truncates every application table except the reference tables.
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
		stats = append(stats, TableStat{Table: t, Rows: n, Reference: isReference(t)})
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
	if err != nil {
		return err
	}
	var quoted []string
	for _, t := range tables {
		if !isReference(t) {
			quoted = append(quoted, quote(t))
		}
	}
	if len(quoted) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, "TRUNCATE "+strings.Join(quoted, ", ")+" RESTART IDENTITY CASCADE"); err != nil {
		return fmt.Errorf("devdata: truncate: %w", err)
	}
	return nil
}
