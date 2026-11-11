//go:build e2e

package e2e

import (
	"context"
	"net/url"
	"sync"
	"testing"

	"github.com/bcc-media/mediabrain/internal/db"
)

// migrationDB creates an empty database and returns its URL and a check for the schema.
func migrationDB(t *testing.T, name string) (string, func() bool) {
	t.Helper()
	ctx := context.Background()

	admin, err := db.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}

	u, _ := url.Parse(dbURL)
	u.Path = "/" + name
	testURL := u.String()

	return testURL, func() bool {
		t.Helper()
		pool, err := db.Connect(ctx, testURL)
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('public.asset') IS NOT NULL`).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
}

// TestMigrationsRoundTrip runs all migrations up, down and up again on a fresh database.
func TestMigrationsRoundTrip(t *testing.T) {
	ctx := context.Background()
	testURL, tableExists := migrationDB(t, "migtest")

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

// TestConcurrentMigrateUp is several instances starting at once with AUTO_MIGRATE on.
func TestConcurrentMigrateUp(t *testing.T) {
	ctx := context.Background()
	testURL, tableExists := migrationDB(t, "migrace")

	const instances = 8
	errs := make([]error, instances)
	var wg sync.WaitGroup
	for i := range instances {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = db.MigrateUp(ctx, testURL)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("instance %d: %v", i, err)
		}
	}
	if !tableExists() {
		t.Fatal("schema missing after concurrent migrate")
	}
}
