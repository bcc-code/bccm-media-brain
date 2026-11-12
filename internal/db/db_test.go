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

func TestPoolConfig(t *testing.T) {
	cfg, err := poolConfig("postgres://u:p@db/x")
	if err != nil {
		t.Fatal(err)
	}
	params := cfg.ConnConfig.RuntimeParams
	if cfg.MaxConns != DefaultMaxConns || params["statement_timeout"] != "35000" || params["idle_in_transaction_session_timeout"] != "60000" {
		t.Fatalf("defaults: %d connections, %v", cfg.MaxConns, params)
	}

	cfg, err = poolConfig("postgres://u:p@db/x?pool_max_conns=3&statement_timeout=1000&idle_in_transaction_session_timeout=0")
	if err != nil {
		t.Fatal(err)
	}
	params = cfg.ConnConfig.RuntimeParams
	if cfg.MaxConns != 3 || params["statement_timeout"] != "1000" || params["idle_in_transaction_session_timeout"] != "0" {
		t.Fatalf("the URL must win: %d connections, %v", cfg.MaxConns, params)
	}
}
