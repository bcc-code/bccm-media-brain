//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bcc-media/mediabrain/internal/cli"
	"github.com/bcc-media/mediabrain/internal/config"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/devdata"
)

// freshDB creates an empty, migrated database and returns its URL.
func freshDB(t *testing.T, name string) string {
	t.Helper()
	ctx := context.Background()
	admin, err := db.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	for _, q := range []string{`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`, `CREATE DATABASE ` + name} {
		if _, err := admin.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	u, _ := url.Parse(dbURL)
	u.Path = "/" + name
	if err := db.MigrateUp(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	return u.String()
}

func newStore(t *testing.T, url string) *devdata.Store {
	t.Helper()
	pool, err := db.Connect(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return devdata.NewStore(pool)
}

func rows(t *testing.T, s *devdata.Store, table string) int64 {
	t.Helper()
	stats, err := s.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range stats {
		if st.Table == table {
			return st.Rows
		}
	}
	t.Fatalf("table %s not in stats %v", table, stats)
	return 0
}

func dummyNames(t *testing.T, url string) []string {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	r, err := pool.Query(ctx, `SELECT id::text || ':' || name FROM dummy ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var out []string
	for r.Next() {
		var n string
		if err := r.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func TestStoreScenarios(t *testing.T) {
	ctx := context.Background()
	url := freshDB(t, "devdata_store")
	s := newStore(t, url)

	// Every registered scenario must run against the real schema.
	for _, sc := range devdata.Scenarios() {
		if _, err := s.Apply(ctx, sc.Name, devdata.ApplyRequest{Reset: true}); err != nil {
			t.Fatalf("scenario %s: %v", sc.Name, err)
		}
	}

	res, err := s.Apply(ctx, "dummies", devdata.ApplyRequest{Reset: true, Params: map[string]string{"count": "15"}})
	if err != nil || rows(t, s, "dummy") != 15 || res.Seed == 0 || !res.Reset {
		t.Fatalf("dummies: %+v, %v", res, err)
	}
	// Additive without reset.
	if _, err := s.Apply(ctx, "dummies", devdata.ApplyRequest{Params: map[string]string{"count": "5"}}); err != nil || rows(t, s, "dummy") != 20 {
		t.Fatalf("additive: %v", err)
	}
	// "empty" always resets.
	if _, err := s.Apply(ctx, "empty", devdata.ApplyRequest{}); err != nil || rows(t, s, "dummy") != 0 {
		t.Fatalf("empty: %v", err)
	}

	// Same seed + reset → identical data (ids restart too).
	req := devdata.ApplyRequest{Reset: true, Seed: 99, Params: map[string]string{"count": "5"}}
	if _, err := s.Apply(ctx, "dummies", req); err != nil {
		t.Fatal(err)
	}
	first := dummyNames(t, url)
	if _, err := s.Apply(ctx, "dummies", req); err != nil {
		t.Fatal(err)
	}
	if second := dummyNames(t, url); strings.Join(first, "|") != strings.Join(second, "|") {
		t.Fatalf("seeded runs differ:\n%v\n%v", first, second)
	}

	// Failures map to typed errors and leave data untouched.
	if _, err := s.Apply(ctx, "nope", devdata.ApplyRequest{}); !errors.Is(err, devdata.ErrUnknownScenario) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := s.Apply(ctx, "dummies", devdata.ApplyRequest{Reset: true, Params: map[string]string{"count": "0"}}); !errors.Is(err, devdata.ErrInvalidParams) {
		t.Fatalf("invalid: %v", err)
	}
	if rows(t, s, "dummy") != 5 {
		t.Fatal("failed apply must not reset data")
	}

	if err := s.Reset(ctx); err != nil || rows(t, s, "dummy") != 0 {
		t.Fatalf("reset: %v", err)
	}
}

func TestStoreOnEmptySchema(t *testing.T) {
	ctx := context.Background()
	url := freshDB(t, "devdata_noschema")
	if err := db.MigrateReset(ctx, url); err != nil {
		t.Fatal(err)
	}
	s := newStore(t, url)
	if stats, err := s.Stats(ctx); err != nil || len(stats) != 0 {
		t.Fatalf("stats = %v, %v", stats, err)
	}
	if err := s.Reset(ctx); err != nil {
		t.Fatalf("reset with no tables: %v", err)
	}
	// Scenario fails (no table) → error surfaces.
	if _, err := s.Apply(ctx, "dummies", devdata.ApplyRequest{}); err == nil {
		t.Fatal("expected error without schema")
	}
}

func TestDebugAPI(t *testing.T) {
	post := func(path, body string) (int, map[string]any) {
		res, err := http.Post(baseURL+"/debug/api/"+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(res.Body).Decode(&m)
		return res.StatusCode, m
	}

	if code, m := post("scenarios/dummies", `{"reset":true,"seed":5,"params":{"count":"7"}}`); code != 200 || m["seed"] != float64(5) {
		t.Fatalf("apply = %d %v", code, m)
	}
	res, err := http.Get(baseURL + "/debug/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	var stats []devdata.TableStat
	_ = json.NewDecoder(res.Body).Decode(&stats)
	res.Body.Close()
	dummies := -1
	for _, s := range stats {
		switch {
		case s.Table == "dummy":
			dummies = int(s.Rows)
		case s.Rows != 0:
			t.Fatalf("table %s has %d rows after reset, want 0", s.Table, s.Rows)
		}
	}
	if dummies != 7 {
		t.Fatalf("dummy rows = %d, want 7 (stats = %+v)", dummies, stats)
	}

	// Data created via the debug API is visible through GraphQL.
	var r struct{ Dummies []dummyJSON }
	mustGQL(t, `{ dummies { id } }`, nil, &r)
	if len(r.Dummies) != 7 {
		t.Fatalf("graphql sees %d dummies", len(r.Dummies))
	}

	if code, _ := post("scenarios/dummies", `{"params":{"count":"-1"}}`); code != 400 {
		t.Fatalf("invalid params = %d", code)
	}
	if code, _ := post("scenarios/nope", `{}`); code != 404 {
		t.Fatalf("unknown = %d", code)
	}
	if code, _ := post("reset", ``); code != 200 {
		t.Fatalf("reset = %d", code)
	}
	res, err = http.Get(baseURL + "/debug/")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("debug ui: %v %v", err, res.StatusCode)
	}
	res.Body.Close()
}

func TestCLIAgainstDB(t *testing.T) {
	url := freshDB(t, "cli_test")
	t.Setenv(config.EnvFileVar, filepath.Join(t.TempDir(), "missing.env"))
	t.Setenv("DATABASE_URL", url)
	ctx := context.Background()
	run := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := cli.Run(ctx, args, &out); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out.String()
	}

	if out := run("migrate", "status"); !strings.Contains(out, "00001_dummy.sql") || !strings.Contains(out, "applied") {
		t.Fatalf("status: %q", out)
	}
	run("migrate", "down")
	if out := run("migrate", "status"); !strings.Contains(out, "pending") {
		t.Fatalf("status after down: %q", out)
	}
	run("migrate", "up")
	run("migrate", "reset")
	if out := run("seed", "dummies", "-reset", "-seed", "3", "count=4"); !strings.Contains(out, "created 4 dummies") || !strings.Contains(out, "seed 3") {
		t.Fatalf("seed: %q", out)
	}
	if n := len(dummyNames(t, url)); n != 4 {
		t.Fatalf("rows after seed = %d", n)
	}
	if out := run("reset-data"); !strings.Contains(out, "reset") {
		t.Fatalf("reset-data: %q", out)
	}
	if n := len(dummyNames(t, url)); n != 0 {
		t.Fatalf("rows after reset = %d", n)
	}
	var out bytes.Buffer
	if err := cli.Run(ctx, []string{"seed", "dummies", "count=0"}, &out); err == nil {
		t.Fatal("expected invalid params error")
	}
}
