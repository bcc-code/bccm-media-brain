package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io"
	"io/fs"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func provider(url string) (*goose.Provider, *sql.DB, error) {
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		return nil, nil, fmt.Errorf("db: open: %w", err)
	}
	sub, _ := fs.Sub(migrationsFS, "migrations") // cannot fail: static, valid path
	// Instances that start together must not both apply a migration: whoever comes second
	// waits on this advisory lock (retrying every second, for up to 5 minutes) and then finds
	// nothing pending.
	locker, _ := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 300)) // cannot fail: valid options
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, sub, goose.WithSessionLocker(locker))
	if err != nil {
		_ = sqlDB.Close()
		return nil, nil, fmt.Errorf("db: goose provider: %w", err)
	}
	return p, sqlDB, nil
}

// MigrateUp applies all pending migrations.
func MigrateUp(ctx context.Context, url string) error {
	p, sqlDB, err := provider(url)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("db: migrate up: %w", err)
	}
	return nil
}

// MigrateReset rolls back all migrations.
func MigrateReset(ctx context.Context, url string) error {
	p, sqlDB, err := provider(url)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	if _, err := p.DownTo(ctx, 0); err != nil {
		return fmt.Errorf("db: migrate reset: %w", err)
	}
	return nil
}

// MigrateDown rolls back the most recent migration.
func MigrateDown(ctx context.Context, url string) error {
	p, sqlDB, err := provider(url)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	if _, err := p.Down(ctx); err != nil {
		return fmt.Errorf("db: migrate down: %w", err)
	}
	return nil
}

// MigrateStatus writes the state of every migration to w.
func MigrateStatus(ctx context.Context, url string, w io.Writer) error {
	p, sqlDB, err := provider(url)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	statuses, err := p.Status(ctx)
	if err != nil {
		return fmt.Errorf("db: migrate status: %w", err)
	}
	for _, s := range statuses {
		applied := "pending"
		if s.State == goose.StateApplied {
			applied = "applied " + s.AppliedAt.Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(w, "%-30s %s\n", s.Source.Path, applied)
	}
	return nil
}
