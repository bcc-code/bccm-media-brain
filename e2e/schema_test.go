//go:build e2e

package e2e

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/bcc-media/mediabrain/internal/db"
)

// Deleting a row looks up what references it, once per deleted row, and Postgres gives the
// referencing column no index by itself. The tables that the API deletes from must have one on
// every column that points at them, or a delete reads whole tables.
func TestDeletedTablesAreReferencedThroughIndexes(t *testing.T) {
	rows, err := pool.Query(context.Background(), `
		SELECT c.conrelid::regclass || '.' || a.attname || ' -> ' || c.confrelid::regclass
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		WHERE c.contype = 'f'
		  AND c.confrelid::regclass::text = ANY($1)
		  AND NOT EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = c.conrelid AND i.indkey[0] = c.conkey[1])
		ORDER BY 1`,
		[]string{"program", "project", "asset", "asset_version", "rendition", "file", "subtitle_track", "storage_location"})
	if err != nil {
		t.Fatal(err)
	}
	missing, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		t.Fatalf("foreign keys without an index on the referencing column: %v", missing)
	}
}

// Every connection of the pool carries the timeouts, so no statement runs and no transaction
// sits open without end, whoever opened it: a request, the sweeper or the CLI.
func TestConnectionsCarryTimeouts(t *testing.T) {
	p, err := db.Connect(context.Background(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var statement, idle string
	err = p.QueryRow(context.Background(),
		`SELECT current_setting('statement_timeout'), current_setting('idle_in_transaction_session_timeout')`).Scan(&statement, &idle)
	if err != nil || statement != "35s" || idle != "1min" {
		t.Fatalf("statement_timeout %q, idle_in_transaction_session_timeout %q (%v)", statement, idle, err)
	}
	if got := p.Config().MaxConns; got != db.DefaultMaxConns {
		t.Fatalf("pool of %d connections, want %d", got, db.DefaultMaxConns)
	}
	if _, err := p.Exec(context.Background(), `SET statement_timeout = 50; SELECT pg_sleep(1)`); err == nil {
		t.Fatal("a statement past its timeout ran to the end")
	}
}
