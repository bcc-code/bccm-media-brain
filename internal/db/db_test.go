package db

import (
	"context"
	"io"
	"testing"
)

const unreachable = "postgres://u:p@127.0.0.1:1/x?sslmode=disable&connect_timeout=1"

func TestConnectErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := Connect(ctx, "::not a url::"); err == nil {
		t.Fatal("expected parse error")
	}
	if _, err := Connect(ctx, unreachable); err == nil {
		t.Fatal("expected ping error")
	}
}

func TestMigrateErrors(t *testing.T) {
	ctx := context.Background()
	if err := MigrateUp(ctx, unreachable); err == nil {
		t.Fatal("expected MigrateUp error")
	}
	if err := MigrateReset(ctx, unreachable); err == nil {
		t.Fatal("expected MigrateReset error")
	}
	if err := MigrateDown(ctx, unreachable); err == nil {
		t.Fatal("expected MigrateDown error")
	}
	if err := MigrateStatus(ctx, unreachable, io.Discard); err == nil {
		t.Fatal("expected MigrateStatus error")
	}
}

func TestEmbeddedMigrations(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil || len(entries) == 0 {
		t.Fatalf("no embedded migrations: %v", err)
	}
}
