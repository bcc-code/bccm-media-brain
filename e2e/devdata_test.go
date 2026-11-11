//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bcc-media/mediabrain/internal/app"
	"github.com/bcc-media/mediabrain/internal/auth"
	"github.com/bcc-media/mediabrain/internal/cli"
	"github.com/bcc-media/mediabrain/internal/config"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/devdata"
	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/internal/fake"
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

// titles lists the generated values that depend on the seed.
func titles(t *testing.T, url string) []string {
	t.Helper()
	ctx := context.Background()
	p, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	r, err := p.Query(ctx, `SELECT title || '/' || coalesce(reel, '') FROM asset
		UNION ALL SELECT code || '/' || name FROM project
		UNION ALL SELECT text FROM subtitle_cue ORDER BY 1`)
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

	small := map[string]string{"programs": "1", "projects": "2", "assets": "3", "users": "2"}
	res, err := s.Apply(ctx, "catalog", devdata.ApplyRequest{Reset: true, Params: small})
	if err != nil || rows(t, s, "asset") != 6 || rows(t, s, "project") != 2 || rows(t, s, "file") != 6 || res.Seed == 0 || !res.Reset {
		t.Fatalf("catalog: %+v, %v", res, err)
	}
	if rows(t, s, "subtitle_cue") == 0 || rows(t, s, "file_copy") < 6 || rows(t, s, "permission_grant") != 2 || rows(t, s, "asset_access") == 0 {
		t.Fatal("catalog should create subtitles, copies, grants and the access they give")
	}
	// Additive without reset.
	if _, err := s.Apply(ctx, "catalog", devdata.ApplyRequest{Params: small}); err != nil || rows(t, s, "asset") != 12 {
		t.Fatalf("additive: %v", err)
	}
	// "empty" always resets.
	if _, err := s.Apply(ctx, "empty", devdata.ApplyRequest{}); err != nil || rows(t, s, "asset") != 0 {
		t.Fatalf("empty: %v", err)
	}

	// Same seed + reset → identical data.
	req := devdata.ApplyRequest{Reset: true, Seed: 99, Params: small}
	if _, err := s.Apply(ctx, "catalog", req); err != nil {
		t.Fatal(err)
	}
	first := titles(t, url)
	if _, err := s.Apply(ctx, "catalog", req); err != nil {
		t.Fatal(err)
	}
	if second := titles(t, url); len(first) == 0 || strings.Join(first, "|") != strings.Join(second, "|") {
		t.Fatalf("seeded runs differ:\n%v\n%v", first, second)
	}

	// Failures map to typed errors and leave data untouched.
	if _, err := s.Apply(ctx, "nope", devdata.ApplyRequest{}); !errors.Is(err, devdata.ErrUnknownScenario) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := s.Apply(ctx, "catalog", devdata.ApplyRequest{Reset: true, Params: map[string]string{"assets": "0"}}); !errors.Is(err, devdata.ErrInvalidParams) {
		t.Fatalf("invalid: %v", err)
	}
	if rows(t, s, "asset") != 6 {
		t.Fatal("failed apply must not reset data")
	}

	// The personas of the access scenario have exactly the access its description promises.
	if _, err := s.Apply(ctx, "access", devdata.ApplyRequest{}); err != nil {
		t.Fatal(err)
	}
	personas := map[string]int{"editor": 6, "viewer": 3, "downloader": 1, "member": 3, "expired": 0, "nobody": 0}
	for name, want := range personas {
		var got int
		err := pool2(t, url).QueryRow(ctx, `SELECT count(DISTINCT aa.asset_id) FROM asset_access aa
			WHERE aa.principal_id IN (SELECT id FROM app_user WHERE email = $1
				UNION SELECT m.group_id FROM user_group_member m JOIN app_user u ON u.id = m.user_id WHERE u.email = $1)`,
			name+"@example.test").Scan(&got)
		if err != nil || got != want {
			t.Errorf("%s can reach %d assets, want %d (%v)", name, got, want, err)
		}
	}
	users, err := s.Users(ctx)
	if err != nil || len(users) != 8 {
		t.Fatalf("users = %d, %v", len(users), err)
	}
	if u, err := s.CreateUser(ctx, "extra@example.test", true); err != nil || !u.Admin || !u.Active {
		t.Fatalf("create user = %+v, %v", u, err)
	}
	if _, err := s.CreateUser(ctx, "extra@example.test", true); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate user: %v", err)
	}

	langs, types := rows(t, s, "language"), rows(t, s, "asset_type")
	if err := s.Reset(ctx); err != nil || rows(t, s, "asset") != 0 || rows(t, s, "app_user") != 0 {
		t.Fatalf("reset: %v", err)
	}
	// Seeded lookup data survives a reset.
	if langs == 0 || rows(t, s, "language") != langs || rows(t, s, "asset_type") != types {
		t.Fatalf("reset touched reference tables: language %d -> %d, asset_type %d -> %d",
			langs, rows(t, s, "language"), types, rows(t, s, "asset_type"))
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
	if _, err := s.Apply(ctx, "catalog", devdata.ApplyRequest{}); err == nil {
		t.Fatal("expected error without schema")
	}
	if _, err := s.Users(ctx); err == nil {
		t.Fatal("expected error without schema")
	}
}

// TestDebugAPI uses its own app, because resetting data would pull the rug from under the
// other tests.
func TestDebugAPI(t *testing.T) {
	cfg := appConfig()
	cfg.DatabaseURL = freshDB(t, "debug_api")
	base := startApp(t, cfg)

	call := func(method, path, body string, out any) int {
		t.Helper()
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if out != nil {
			_ = json.NewDecoder(res.Body).Decode(out)
		}
		return res.StatusCode
	}

	var applied map[string]any
	if code := call("POST", "/debug/api/scenarios/access", `{"seed":5}`, &applied); code != 200 || applied["seed"] != float64(5) || applied["reset"] != true {
		t.Fatalf("apply = %d %v", code, applied)
	}
	var stats []devdata.TableStat
	call("GET", "/debug/api/stats", "", &stats)
	counts := map[string]int64{}
	for _, s := range stats {
		counts[s.Table] = s.Rows
		if s.Reference && s.Rows == 0 {
			t.Fatalf("reference table %s was emptied by reset", s.Table)
		}
	}
	if counts["asset"] != 6 || counts["app_user"] != 8 {
		t.Fatalf("stats = %v", counts)
	}

	// Acting as a persona through the debug cookie shows exactly what that persona may see.
	var users []devdata.User
	if code := call("GET", "/debug/api/users", "", &users); code != 200 || len(users) != 8 {
		t.Fatalf("users = %d %v", code, users)
	}
	seen := func(cookie string) (int, int) {
		req, _ := http.NewRequest("POST", base+"/query", strings.NewReader(`{"query":"{ assets { id } }"}`))
		req.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: auth.DebugUserCookie, Value: cookie})
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var r struct {
			Data struct{ Assets []struct{ ID string } }
		}
		_ = json.NewDecoder(res.Body).Decode(&r)
		return res.StatusCode, len(r.Data.Assets)
	}
	want := map[string]int{"admin@example.test": 6, "editor@example.test": 6, "viewer@example.test": 3, "nobody@example.test": 0}
	for _, u := range users {
		if n, ok := want[*u.Email]; ok {
			if code, got := seen(u.ID); code != 200 || got != n {
				t.Errorf("%s: status %d, sees %d assets, want %d", *u.Email, code, got, n)
			}
		}
		if *u.Email == "inactive@example.test" {
			if code, _ := seen(u.ID); code != http.StatusForbidden {
				t.Errorf("inactive persona: status %d", code)
			}
		}
	}
	if code, _ := seen(""); code != http.StatusUnauthorized {
		t.Fatalf("no user chosen: status %d", code)
	}

	var created devdata.User
	if code := call("POST", "/debug/api/users", `{"email":"new@example.test","admin":true}`, &created); code != 200 || !created.Admin {
		t.Fatalf("create user = %d %+v", code, created)
	}
	if code, n := seen(created.ID); code != 200 || n != 6 {
		t.Fatalf("new admin sees %d assets (status %d)", n, code)
	}
	if code := call("POST", "/debug/api/users", `{"email":"new@example.test"}`, nil); code != 400 {
		t.Fatalf("duplicate user = %d", code)
	}
	if code := call("POST", "/debug/api/users", `{"email":" "}`, nil); code != 400 {
		t.Fatalf("blank email = %d", code)
	}

	// Vidispine: import and refresh from the debug UI, as the system principal.
	var status map[string]bool
	if code := call("GET", "/debug/api/vidispine", "", &status); code != 200 || !status["configured"] {
		t.Fatalf("vidispine status = %d %v", code, status)
	}
	item := vidispineItem(fake.New(11), newVidispineStorage())
	var imported devdata.VidispineAsset
	if code := call("POST", "/debug/api/vidispine/import", fmt.Sprintf(`{"itemId":%q,"typeId":1}`, item.ID), &imported); code != 200 ||
		len(imported.LegacyIDs) != 1 || imported.LegacyIDs[0] != item.ID || !strings.Contains(string(imported.Metadata), item.ID) {
		t.Fatalf("vidispine import = %d %+v", code, imported)
	}
	if code := call("POST", "/debug/api/vidispine/import", fmt.Sprintf(`{"itemId":%q,"typeId":1}`, item.ID), nil); code != http.StatusConflict {
		t.Fatalf("second import = %d", code)
	}
	vsServer.SetField(item.ID, "title", "Changed in Vidispine")
	var refreshed devdata.VidispineAsset
	if code := call("POST", "/debug/api/vidispine/refresh", fmt.Sprintf(`{"assetId":%q}`, imported.ID), &refreshed); code != 200 ||
		refreshed.Title != imported.Title || !strings.Contains(string(refreshed.Metadata), "Changed in Vidispine") {
		t.Fatalf("vidispine refresh = %d %+v", code, refreshed)
	}
	if code := call("POST", "/debug/api/vidispine/import", `{"itemId":"nope","typeId":1}`, nil); code != 400 {
		t.Fatalf("bad item id = %d", code)
	}
	if code := call("POST", "/debug/api/vidispine/refresh", `{"assetId":"AS-01ARZ3NDEKTSV4RRFFQ69G5FAV"}`, nil); code != 404 {
		t.Fatalf("refresh of unknown asset = %d", code)
	}

	if code := call("POST", "/debug/api/scenarios/catalog", `{"params":{"assets":"-1"}}`, nil); code != 400 {
		t.Fatalf("invalid params = %d", code)
	}
	if code := call("POST", "/debug/api/scenarios/nope", `{}`, nil); code != 404 {
		t.Fatalf("unknown = %d", code)
	}
	if code := call("POST", "/debug/api/reset", ``, nil); code != 200 {
		t.Fatalf("reset = %d", code)
	}
	if code := call("GET", "/debug/", "", nil); code != 200 {
		t.Fatalf("debug ui = %d", code)
	}
	if code := call("GET", "/", "", nil); code != 200 {
		t.Fatalf("playground = %d", code)
	}
	if code := call("GET", "/healthz", "", nil); code != 200 {
		t.Fatalf("healthz = %d", code)
	}
}

// startApp runs a second server for the test and returns its base URL.
func startApp(t *testing.T, cfg config.Config) string {
	t.Helper()
	a, err := app.New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx) }()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return "http://" + a.Addr()
}

// TestDefaultDebugUser is the `make dev` setup: no proxy, DEBUG_USER set. Requests that name
// nobody work on an empty database and keep working after the data is wiped.
func TestDefaultDebugUser(t *testing.T) {
	cfg := config.Config{
		DatabaseURL: freshDB(t, "debug_user"), HTTPAddr: "127.0.0.1:0", Playground: true,
		Debug: true, DebugUser: "admin@example.test", AuthHeader: "Authorization",
	}
	base := startApp(t, cfg)
	me := func(headers map[string]string) (int, string, bool) {
		t.Helper()
		req, _ := http.NewRequest("POST", base+"/query", strings.NewReader(`{"query":"{ me { id email isAdmin } }"}`))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var r struct {
			Data struct {
				Me struct {
					ID, Email string
					IsAdmin   bool
				}
			}
		}
		_ = json.NewDecoder(res.Body).Decode(&r)
		return res.StatusCode, r.Data.Me.ID + "/" + r.Data.Me.Email, r.Data.Me.IsAdmin
	}

	status, first, admin := me(nil)
	if status != 200 || !strings.HasSuffix(first, "/admin@example.test") || !admin {
		t.Fatalf("empty database: %d %q admin=%v", status, first, admin)
	}
	// Naming the default user explicitly (as the Yaak workspace does) is the same user.
	if status, again, _ := me(map[string]string{auth.DebugUserHeader: "admin@example.test"}); status != 200 || again != first {
		t.Fatalf("named: %d %q, want %q", status, again, first)
	}
	// Other users still have to exist.
	if status, _, _ := me(map[string]string{auth.DebugUserHeader: "someone@example.test"}); status != http.StatusUnauthorized {
		t.Fatalf("unknown user: %d", status)
	}

	// The user comes back after a reset, and the access scenario's own admin is reused.
	for _, path := range []string{"/debug/api/reset", "/debug/api/scenarios/access"} {
		res, err := http.Post(base+path, "application/json", strings.NewReader("{}"))
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("%s: %v", path, err)
		}
		res.Body.Close()
		if status, got, admin := me(nil); status != 200 || !strings.HasSuffix(got, "/admin@example.test") || !admin {
			t.Fatalf("after %s: %d %q admin=%v", path, status, got, admin)
		}
	}
}

func pool2(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	p, err := db.Connect(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
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

	if out := run("migrate", "status"); !strings.Contains(out, "00007_user_admin.sql") || !strings.Contains(out, "applied") {
		t.Fatalf("status: %q", out)
	}
	run("migrate", "down")
	if out := run("migrate", "status"); !strings.Contains(out, "pending") {
		t.Fatalf("status after down: %q", out)
	}
	run("migrate", "up")
	run("migrate", "reset")
	run("migrate", "up")
	if out := run("seed", "catalog", "-reset", "-seed", "3", "programs=1", "projects=1", "assets=4"); !strings.Contains(out, "1 projects, 4 assets") || !strings.Contains(out, "seed 3") {
		t.Fatalf("seed: %q", out)
	}
	if n := len(titles(t, url)); n < 5 {
		t.Fatalf("rows after seed = %d", n)
	}
	if out := run("reset-data"); !strings.Contains(out, "reset") {
		t.Fatalf("reset-data: %q", out)
	}
	if n := len(titles(t, url)); n != 0 {
		t.Fatalf("rows after reset = %d", n)
	}
	var out bytes.Buffer
	if err := cli.Run(ctx, []string{"seed", "catalog", "assets=0"}, &out); err == nil {
		t.Fatal("expected invalid params error")
	}
}
