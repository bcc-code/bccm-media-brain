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
	const list = `{ assets(first: 1) { nodes { id } } }`
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
	const levels = `{ nodes { id versions { nodes { id renditions { id files { role file { id } } } } } } }`
	if r, _ := exec(t, admin, 0, `{ assets(first: 100) `+levels+` }`, nil); len(r.Errors) != 0 {
		t.Fatalf("a page of 100 assets: %+v", r.Errors)
	}
	r, statements := exec(t, admin, 0, `{ assets(first: 500) `+levels+` }`, nil)
	if len(r.Errors) != 1 || r.Errors[0].Extensions.Code != "COMPLEXITY_LIMIT_EXCEEDED" || !strings.Contains(r.Errors[0].Message, fmt.Sprint(graph.ComplexityLimit)) {
		t.Fatalf("500 assets with nested lists: %+v", r.Errors)
	}
	if statements != 0 {
		t.Fatalf("a refused query ran %d statements", statements)
	}
	// The same page is fine once the nested list is short.
	if r, _ := exec(t, admin, 0, `{ assets(first: 500) { nodes { id versions(first: 5) { nodes { id renditions { id files { role file { id } } } } } } } }`, nil); len(r.Errors) != 0 {
		t.Fatalf("500 assets with 5 versions each: %+v", r.Errors)
	}
}

// pageInfo is where a page ends, as every connection reports it.
type pageInfo struct {
	HasNextPage bool
	EndCursor   *string
}

// after is the arguments for the page that follows the one info ended.
func after(first int, info pageInfo) string {
	return fmt.Sprintf(`(first: %d, after: %q)`, first, *info.EndCursor)
}

func TestNestedListsArePaged(t *testing.T) {
	w := newWorld(t, 53)
	// c1 gets versions 2 to 4, c2 keeps its one.
	for range 3 {
		newVersion(t, w.admin, w.c1.asset)
	}
	const versionPage = ` { nodes { number } pageInfo { hasNextPage endCursor } } } }`
	numbers := func(u user, asset, args string) (string, pageInfo) {
		t.Helper()
		var out struct {
			Asset struct {
				Versions struct {
					Nodes    []struct{ Number int }
					PageInfo pageInfo
				}
			}
		}
		u.must(t, `query($id: ID!) { asset(id: $id) { versions`+args+versionPage, map[string]any{"id": asset}, &out)
		var got []string
		for _, v := range out.Asset.Versions.Nodes {
			got = append(got, fmt.Sprint(v.Number))
		}
		return strings.Join(got, ","), out.Asset.Versions.PageInfo
	}
	for _, args := range []string{"", "(first: 500)", "(first: null)", "(first: 0)", "(first: 4)", `(after: "")`, "(after: null)"} {
		if got, info := numbers(w.admin, w.c1.asset, args); got != "1,2,3,4" || info.HasNextPage || info.EndCursor == nil {
			t.Errorf("versions%s = %q, %+v; want all four and no next page", args, got, info)
		}
	}
	// Page by page: each cursor leads to what follows, a full last page has no next one, and
	// past the end there is an empty page without a cursor.
	first, info := numbers(w.admin, w.c1.asset, "(first: 2)")
	if first != "1,2" || !info.HasNextPage {
		t.Fatalf("the first two = %q, %+v", first, info)
	}
	second, info := numbers(w.admin, w.c1.asset, after(2, info))
	if second != "3,4" || info.HasNextPage {
		t.Fatalf("the next two = %q, %+v", second, info)
	}
	if past, end := numbers(w.admin, w.c1.asset, after(2, info)); past != "" || end.HasNextPage || end.EndCursor != nil {
		t.Fatalf("past the end = %q, %+v", past, end)
	}
	three, info := numbers(w.admin, w.c1.asset, "(first: 3)")
	if rest, end := numbers(w.admin, w.c1.asset, after(3, info)); three != "1,2,3" || !info.HasNextPage || rest != "4" || end.HasNextPage {
		t.Fatalf("three and the rest = %q, %q, %+v", three, rest, end)
	}

	// Projects of a program: by code, and a page counts only what the caller may see.
	for range 3 {
		newProject(t, w.admin, w.f, w.prog)
	}
	codes := func(u user, args string) ([]string, pageInfo) {
		t.Helper()
		var out struct {
			Program struct {
				Projects struct {
					Nodes    []struct{ ID, Code string }
					PageInfo pageInfo
				}
			}
		}
		u.must(t, `query($id: ID!) { program(id: $id) { projects`+args+` { nodes { id code } pageInfo { hasNextPage endCursor } } } }`,
			map[string]any{"id": w.prog.ID}, &out)
		var got []string
		for _, p := range out.Program.Projects.Nodes {
			got = append(got, p.Code)
		}
		return got, out.Program.Projects.PageInfo
	}
	all, end := codes(w.admin, "")
	if len(all) != 5 || !sortedStrings(all) || end.HasNextPage {
		t.Fatalf("projects = %v, %+v; want 5 by code", all, end)
	}
	var walked []string
	projectPage, pages := "(first: 2)", 0
	for {
		got, info := codes(w.admin, projectPage)
		walked, pages = append(walked, got...), pages+1
		if !info.HasNextPage {
			break
		}
		projectPage = after(2, info)
	}
	if strings.Join(walked, ",") != strings.Join(all, ",") || pages != 3 {
		t.Errorf("walking the projects two at a time gave %v in %d pages, want %v in 3", walked, pages, all)
	}

	// A cursor is for the list that gave it. One from a list sorted by text is refused by a list
	// sorted by number, and so is anything that is not a cursor.
	_, projectsEnd := codes(w.admin, "(first: 1)")
	for args, field := range map[string]string{
		"(first: 501)": "first", "(first: -1)": "first", `(after: "not a cursor!")`: "after", after(1, projectsEnd): "after",
	} {
		e := w.admin.fails(t, "INVALID_INPUT", `query($id: ID!) { asset(id: $id) { versions`+args+versionPage, map[string]any{"id": w.c1.asset})
		if e.Extensions.Field != field {
			t.Errorf("versions%s: the error names the field %q, want %q", args, e.Extensions.Field, field)
		}
	}
	for _, args := range []string{"(first: 501)", "(first: -1)", `(after: "not a cursor!")`} {
		w.admin.fails(t, "INVALID_INPUT", `query($id: ID!) { program(id: $id) { projects`+args+` { nodes { id } } } }`, map[string]any{"id": w.prog.ID})
	}

	// The page is taken within each asset, and two pages of one field are two batches.
	_, firstVersion := numbers(w.admin, w.c1.asset, "(first: 1)")
	var page struct {
		Assets struct {
			Nodes []struct {
				ID    string
				First struct{ Nodes []struct{ Number int } }
				Rest  struct {
					Nodes    []struct{ Number int }
					PageInfo pageInfo
				}
			}
		}
	}
	w.admin.must(t, `query($t: String, $c: String) { assets(filter: {title: $t}) { nodes { id
		first: versions(first: 1) { nodes { number } }
		rest: versions(first: 2, after: $c) { nodes { number } pageInfo { hasNextPage } } } } }`,
		map[string]any{"t": w.marker, "c": *firstVersion.EndCursor}, &page)
	for _, a := range page.Assets.Nodes {
		want, more := 0, false
		if a.ID == w.c1.asset {
			want, more = 2, true
		}
		if len(a.First.Nodes) != 1 || a.First.Nodes[0].Number != 1 || len(a.Rest.Nodes) != want || a.Rest.PageInfo.HasNextPage != more {
			t.Errorf("asset %s: first %v, rest %+v", a.ID, a.First, a.Rest)
		}
	}
	if len(page.Assets.Nodes) != 3 {
		t.Fatalf("%d assets", len(page.Assets.Nodes))
	}

	// A member of the last project only: it is the first of their page, not the fifth.
	member := signIn(t)
	var last struct {
		Projects struct{ Nodes []struct{ ID, Code string } }
	}
	w.admin.must(t, `query($p: ID) { projects(programId: $p, first: 500) { nodes { id code } } }`, map[string]any{"p": w.prog.ID}, &last)
	lastProject := last.Projects.Nodes[len(last.Projects.Nodes)-1]
	if lastProject.Code != all[4] {
		t.Fatalf("root list ends with %s, nested list with %s", lastProject.Code, all[4])
	}
	grant(t, w.admin, "PROJECT", lastProject.ID, "VIEW", to(member))
	if got, info := codes(member, "(first: 1)"); len(got) != 1 || got[0] != all[4] || info.HasNextPage {
		t.Errorf("the member's page = %v, %+v; want %s and nothing after it", got, info, all[4])
	}
}

// Root lists page by cursor. What a cursor leads to does not move when something is added in
// front: with offsets, a new asset pushed the last one of a page onto the next page as well.
func TestRootListsArePagedByCursor(t *testing.T) {
	w := newWorld(t, 54)
	type assets struct {
		Assets struct {
			Nodes    []struct{ ID string }
			PageInfo pageInfo
		}
	}
	const query = `query($t: String, $n: Int, $c: String) { assets(filter: {title: $t}, first: $n, after: $c) { nodes { id } pageInfo { hasNextPage endCursor } } }`
	var everything, first, rest assets
	w.admin.must(t, query, map[string]any{"t": w.marker}, &everything)
	if len(everything.Assets.Nodes) != 3 || everything.Assets.PageInfo.HasNextPage {
		t.Fatalf("the world has %+v", everything.Assets)
	}
	w.admin.must(t, query, map[string]any{"t": w.marker, "n": 2}, &first)
	if len(first.Assets.Nodes) != 2 || !first.Assets.PageInfo.HasNextPage {
		t.Fatalf("the first page = %+v", first.Assets)
	}
	// Newest first, so this one goes in front of the page already read.
	added := newAsset(t, w.admin, w.f, w.projectA)
	w.admin.must(t, updateAsset, map[string]any{"id": added, "in": map[string]any{"typeId": 1, "title": w.marker + " added"}}, nil)
	w.admin.must(t, query, map[string]any{"t": w.marker, "n": 2, "c": *first.Assets.PageInfo.EndCursor}, &rest)
	var got []string
	for _, a := range append(first.Assets.Nodes, rest.Assets.Nodes...) {
		got = append(got, a.ID)
	}
	var want []string
	for _, a := range everything.Assets.Nodes {
		want = append(want, a.ID)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") || rest.Assets.PageInfo.HasNextPage || strings.Contains(strings.Join(got, ","), added) {
		t.Fatalf("two pages gave %v, want %v (the new asset is %s)", got, want, added)
	}

	// The other root lists take the same arguments; each sorts by its own key.
	for field, selection := range map[string]string{"files": "id", "users": "id", "programs": "code", "projects": "code"} {
		var out map[string]struct {
			Nodes    []map[string]string
			PageInfo pageInfo
		}
		w.admin.must(t, `{ `+field+`(first: 1) { nodes { `+selection+` } pageInfo { hasNextPage endCursor } } }`, nil, &out)
		one := out[field]
		if len(one.Nodes) != 1 || !one.PageInfo.HasNextPage {
			t.Fatalf("%s(first: 1) = %+v", field, one)
		}
		w.admin.must(t, `{ `+field+after(1, one.PageInfo)+` { nodes { `+selection+` } pageInfo { hasNextPage endCursor } } }`, nil, &out)
		if two := out[field]; len(two.Nodes) != 1 || two.Nodes[0][selection] == one.Nodes[0][selection] {
			t.Errorf("%s: the page after %v is %v", field, one.Nodes, two.Nodes)
		}
		w.admin.fails(t, "INVALID_INPUT", `{ `+field+`(after: "not a cursor!") { nodes { `+selection+` } } }`, nil)
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
