//go:build e2e

package e2e

import (
	"context"
	"net/url"
	"testing"

	"github.com/bcc-media/mediabrain/internal/db"
)

// TestMigrationsRoundTrip runs all migrations up, down and up again on a fresh database.
func TestMigrationsRoundTrip(t *testing.T) {
	ctx := context.Background()

	admin, err := db.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS migtest WITH (FORCE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE migtest`); err != nil {
		t.Fatal(err)
	}

	u, _ := url.Parse(dbURL)
	u.Path = "/migtest"
	testURL := u.String()

	tableExists := func() bool {
		pool, err := db.Connect(ctx, testURL)
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('public.dummy') IS NOT NULL`).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}

	steps := []struct {
		name string
		fn   func(context.Context, string) error
		want bool
	}{
		{"up", db.MigrateUp, true},
		{"up again is a no-op", db.MigrateUp, true},
		{"reset", db.MigrateReset, false},
		{"up after reset", db.MigrateUp, true},
	}
	for _, s := range steps {
		if err := s.fn(ctx, testURL); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if got := tableExists(); got != s.want {
			t.Fatalf("%s: table exists = %v, want %v", s.name, got, s.want)
		}
	}
}
