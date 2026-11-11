//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/app"
	"github.com/bcc-media/mediabrain/internal/auth"
	"github.com/bcc-media/mediabrain/internal/graph"
)

// TestQueryDeadline holds a lock on the asset table so that statements on it wait, and checks
// that a request stops at its deadline with TIMEOUT, changes nothing and leaves no connection
// behind.
func TestQueryDeadline(t *testing.T) {
	w := newWorld(t, 51)
	ctx := context.Background()
	resolver := probedServices(&probe{})
	const deadline = 300 * time.Millisecond
	srv := httptest.NewServer(app.NewHandler(resolver, pool, app.HandlerOptions{
		QueryTimeout: deadline,
		Auth:         auth.Options{Users: resolver.AccessSvc, Debug: true},
	}))
	defer srv.Close()
	ask := func(query string, vars map[string]any) (gqlResponse, time.Duration) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/query", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(auth.DebugUserHeader, w.admin.id)
		start := time.Now()
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var r gqlResponse
		if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
			t.Fatal(err)
		}
		return r, time.Since(start)
	}
	title := func() string {
		var s string
		if err := pool.QueryRow(ctx, `SELECT title FROM asset WHERE id = $1`, w.c1.asset).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	const list = `{ assets(limit: 1) { id } }`
	if r, _ := ask(list, nil); len(r.Errors) != 0 {
		t.Fatalf("before the lock: %+v", r.Errors)
	}
	before := title()

	var logged bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	defer slog.SetDefault(old)

	lock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(ctx) }()
	if _, err := lock.Exec(ctx, `LOCK TABLE asset IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		query string
		vars  map[string]any
	}{
		"query":    {list, nil},
		"mutation": {updateAsset, map[string]any{"id": w.c1.asset, "in": map[string]any{"typeId": 1, "title": "written past the deadline"}}},
	} {
		r, took := ask(c.query, c.vars)
		if len(r.Errors) != 1 || r.Errors[0].Extensions.Code != "TIMEOUT" || r.Errors[0].Message != "the request took too long" {
			t.Fatalf("%s: errors = %+v, want one TIMEOUT that says nothing else", name, r.Errors)
		}
		if took < deadline || took > deadline+3*time.Second {
			t.Fatalf("%s: answered after %v with a deadline of %v", name, took, deadline)
		}
	}
	if n := strings.Count(logged.String(), "request stopped at its deadline"); n != 2 {
		t.Fatalf("%d deadline log lines, want 2:\n%s", n, logged.String())
	}

	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := ask(list, nil); len(r.Errors) != 0 {
		t.Fatalf("after the lock: %+v", r.Errors)
	}
	if got := title(); got != before {
		t.Fatalf("the mutation that timed out changed the title to %q", got)
	}
	// The cancelled statements gave their connections back.
	for wait := time.Now().Add(3 * time.Second); pool.Stat().AcquiredConns() != 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(wait) {
			t.Fatalf("%d connections still in use", pool.Stat().AcquiredConns())
		}
	}
}

// TestComplexityLimit: 100 assets with versions, renditions and files cost 860 201, 500 of
// them 4 301 001 (internal/graph/complexity_test.go has the sums). The limit is one million.
func TestComplexityLimit(t *testing.T) {
	w := newWorld(t, 52)
	admin := access.Principal{UserID: w.admin.id, Admin: true}
	const levels = `{ id versions { id renditions { id files { role file { id } } } } }`
	if r, _ := exec(t, admin, 0, `{ assets(limit: 100) `+levels+` }`, nil); len(r.Errors) != 0 {
		t.Fatalf("a page of 100 assets: %+v", r.Errors)
	}
	r, statements := exec(t, admin, 0, `{ assets(limit: 500) `+levels+` }`, nil)
	if len(r.Errors) != 1 || r.Errors[0].Extensions.Code != "COMPLEXITY_LIMIT_EXCEEDED" || !strings.Contains(r.Errors[0].Message, fmt.Sprint(graph.ComplexityLimit)) {
		t.Fatalf("500 assets with nested lists: %+v", r.Errors)
	}
	if statements != 0 {
		t.Fatalf("a refused query ran %d statements", statements)
	}
	// The same page is fine once the nested list is short.
	if r, _ := exec(t, admin, 0, `{ assets(limit: 500) { id versions(limit: 5) { id renditions { id files { role file { id } } } } } }`, nil); len(r.Errors) != 0 {
		t.Fatalf("500 assets with 5 versions each: %+v", r.Errors)
	}
}

func TestNestedListsArePaged(t *testing.T) {
	w := newWorld(t, 53)
	// c1 gets versions 2 to 4, c2 keeps its one.
	for range 3 {
		newVersion(t, w.admin, w.c1.asset)
	}
	numbers := func(u user, asset, args string) string {
		t.Helper()
		var out struct {
			Asset struct{ Versions []struct{ Number int } }
		}
		u.must(t, `query($id: ID!) { asset(id: $id) { versions`+args+` { number } } }`, map[string]any{"id": asset}, &out)
		var got []string
		for _, v := range out.Asset.Versions {
			got = append(got, fmt.Sprint(v.Number))
		}
		return strings.Join(got, ",")
	}
	for args, want := range map[string]string{
		"":                       "1,2,3,4",
		"(limit: 2)":             "1,2",
		"(limit: 2, offset: 1)":  "2,3",
		"(limit: 2, offset: 3)":  "4",
		"(offset: 4)":            "",
		"(limit: 500)":           "1,2,3,4",
		"(limit: null)":          "1,2,3,4",
		"(limit: 0, offset: 0)":  "1,2,3,4",
		"(limit: 1, offset: 99)": "",
	} {
		if got := numbers(w.admin, w.c1.asset, args); got != want {
			t.Errorf("versions%s = %q, want %q", args, got, want)
		}
	}
	for _, args := range []string{"(limit: 501)", "(limit: -1)", "(offset: -1)"} {
		w.admin.fails(t, "INVALID_INPUT", `query($id: ID!) { asset(id: $id) { versions`+args+` { number } } }`, map[string]any{"id": w.c1.asset})
		w.admin.fails(t, "INVALID_INPUT", `query($id: ID!) { program(id: $id) { projects`+args+` { id } } }`, map[string]any{"id": w.prog.ID})
	}

	// The page is taken within each asset, and two pages of one field are two batches.
	var page struct {
		Assets []struct {
			ID    string
			First []struct{ Number int }
			Rest  []struct{ Number int }
		}
	}
	w.admin.must(t, `query($t: String) { assets(filter: {title: $t}) { id first: versions(limit: 1) { number } rest: versions(limit: 10, offset: 1) { number } } }`,
		map[string]any{"t": w.marker}, &page)
	for _, a := range page.Assets {
		want := 0
		if a.ID == w.c1.asset {
			want = 3
		}
		if len(a.First) != 1 || a.First[0].Number != 1 || len(a.Rest) != want {
			t.Errorf("asset %s: first %v, rest %v", a.ID, a.First, a.Rest)
		}
	}
	if len(page.Assets) != 3 {
		t.Fatalf("%d assets", len(page.Assets))
	}

	// Projects of a program: by code, and a page counts only what the caller may see.
	for range 3 {
		newProject(t, w.admin, w.f, w.prog)
	}
	codes := func(u user, args string) []string {
		t.Helper()
		var out struct {
			Program struct{ Projects []struct{ ID, Code string } }
		}
		u.must(t, `query($id: ID!) { program(id: $id) { projects`+args+` { id code } } }`, map[string]any{"id": w.prog.ID}, &out)
		var got []string
		for _, p := range out.Program.Projects {
			got = append(got, p.Code)
		}
		return got
	}
	all := codes(w.admin, "")
	if len(all) != 5 || !sortedStrings(all) {
		t.Fatalf("projects = %v, want 5 by code", all)
	}
	if got := codes(w.admin, "(limit: 2, offset: 1)"); strings.Join(got, ",") != strings.Join(all[1:3], ",") {
		t.Errorf("second and third project = %v, want %v", got, all[1:3])
	}
	if got := codes(w.admin, "(offset: 5)"); len(got) != 0 {
		t.Errorf("past the end = %v", got)
	}
	// A member of the last project only: it is the first of their page, not the fifth.
	member := signIn(t)
	var last struct{ Projects []struct{ ID, Code string } }
	w.admin.must(t, `query($p: ID) { projects(programId: $p, limit: 500) { id code } }`, map[string]any{"p": w.prog.ID}, &last)
	lastProject := last.Projects[len(last.Projects)-1]
	if lastProject.Code != all[4] {
		t.Fatalf("root list ends with %s, nested list with %s", lastProject.Code, all[4])
	}
	grant(t, w.admin, "PROJECT", lastProject.ID, "VIEW", to(member))
	if got := codes(member, "(limit: 1)"); len(got) != 1 || got[0] != all[4] {
		t.Errorf("the member's first project = %v, want %s", got, all[4])
	}
	if got := codes(member, "(limit: 1, offset: 1)"); len(got) != 0 {
		t.Errorf("the member's second project = %v, want none", got)
	}
}

func sortedStrings(s []string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] > s[i] {
			return false
		}
	}
	return true
}
