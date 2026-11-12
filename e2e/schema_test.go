//go:build e2e

package e2e

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
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
