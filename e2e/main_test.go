//go:build e2e

// Package e2e runs the full application against a real Postgres in docker. Requests carry
// real JWTs signed by a test issuer that stands in for the auth proxy.
package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bcc-media/mediabrain/internal/app"
	"github.com/bcc-media/mediabrain/internal/auth/authtest"
	"github.com/bcc-media/mediabrain/internal/config"
	"github.com/bcc-media/mediabrain/internal/db"
)

var (
	dbURL   string // connection string of the test container
	baseURL string // http://host:port of the running app
	issuer  *authtest.Issuer
	pool    *pgxpool.Pool // direct database access for test setup and assertions
)

func TestMain(m *testing.M) { os.Exit(run(m)) }

func appConfig() config.Config {
	return config.Config{
		DatabaseURL: dbURL, HTTPAddr: "127.0.0.1:0", AutoMigrate: true, Playground: true, Debug: true,
		AuthHeader: "Authorization", AuthGroupsClaim: "groups", AuthTrustedProxies: "127.0.0.1",
		AuthIssuer: issuer.URL, AuthAudience: authtest.Audience, AuthJWKSURL: issuer.JWKSURL,
	}
}

func run(m *testing.M) int {
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "pgvector/pgvector:pg17",
		tcpostgres.WithDatabase("mediabrain"),
		tcpostgres.WithUsername("mediabrain"),
		tcpostgres.WithPassword("mediabrain"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Printf("start postgres container: %v", err)
		return 1
	}
	defer func() { _ = testcontainers.TerminateContainer(pg) }()

	if dbURL, err = pg.ConnectionString(ctx, "sslmode=disable"); err != nil {
		log.Printf("connection string: %v", err)
		return 1
	}
	issuer = authtest.NewIssuer()
	defer issuer.Close()

	a, err := app.New(ctx, appConfig())
	if err != nil {
		log.Printf("start app: %v", err)
		return 1
	}
	appCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- a.Serve(appCtx) }()
	baseURL = "http://" + a.Addr()

	if pool, err = db.Connect(ctx, dbURL); err != nil {
		log.Printf("connect: %v", err)
		return 1
	}
	code := m.Run()
	pool.Close()

	stop()
	if err := <-done; err != nil {
		log.Printf("app shutdown: %v", err)
		return 1
	}
	return code
}

type gqlError struct {
	Message    string `json:"message"`
	Path       []any  `json:"path"`
	Extensions struct {
		Code string `json:"code"`
	} `json:"extensions"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
}

// post sends a GraphQL request with the given headers and returns the HTTP status and body.
func post(t *testing.T, headers map[string]string, query string, vars map[string]any) (int, gqlResponse) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/query", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /query: %v", err)
	}
	defer res.Body.Close()
	var r gqlResponse
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		t.Fatalf("decode response (status %d): %v", res.StatusCode, err)
	}
	return res.StatusCode, r
}

// user is someone who has signed in through the (test) auth proxy.
type user struct {
	id, sub, email, token string
}

func unique() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func tokenFor(sub, email string, groups ...string) string {
	return issuer.Token(sub, map[string]any{"email": email, "name": "Test " + sub, "groups": groups})
}

// signIn creates a fresh user by making their first request.
func signIn(t *testing.T, groups ...string) user {
	t.Helper()
	id := unique()
	u := user{sub: "e2e|" + id, email: "user-" + id + "@example.test"}
	u.token = tokenFor(u.sub, u.email, groups...)
	var me struct{ Me struct{ ID string } }
	u.must(t, `{ me { id } }`, nil, &me)
	u.id = me.Me.ID
	return u
}

// signInAdmin creates a fresh system administrator.
func signInAdmin(t *testing.T) user {
	t.Helper()
	u := signIn(t)
	if _, err := pool.Exec(context.Background(), `UPDATE app_user SET is_admin = true WHERE id = $1`, u.id); err != nil {
		t.Fatal(err)
	}
	// A new token, because the principal of the old one is cached for a minute.
	u.token = tokenFor(u.sub, u.email)
	return u
}

// gql runs a query as the user and decodes data into out (if non-nil).
func (u user) gql(t *testing.T, query string, vars map[string]any, out any) []gqlError {
	t.Helper()
	status, r := post(t, map[string]string{"Authorization": "Bearer " + u.token}, query, vars)
	if status != http.StatusOK && status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d: %+v", status, r.Errors)
	}
	if out != nil && len(r.Data) > 0 && string(r.Data) != "null" {
		if err := json.Unmarshal(r.Data, out); err != nil {
			t.Fatalf("decode data %s: %v", r.Data, err)
		}
	}
	return r.Errors
}

// must is gql that fails the test on GraphQL errors.
func (u user) must(t *testing.T, query string, vars map[string]any, out any) {
	t.Helper()
	if errs := u.gql(t, query, vars, out); len(errs) > 0 {
		t.Fatalf("graphql errors: %+v\nquery: %s\nvars: %v", errs, query, vars)
	}
}

// fails asserts that the query is refused with the given error code.
func (u user) fails(t *testing.T, code, query string, vars map[string]any) gqlError {
	t.Helper()
	errs := u.gql(t, query, vars, nil)
	if len(errs) == 0 || errs[0].Extensions.Code != code {
		t.Fatalf("want %s, got %+v\nquery: %s\nvars: %v", code, errs, query, vars)
	}
	return errs[0]
}

// isNull asserts that a lookup by id returns null without an error: the object does not
// exist, or the user may not know that it does.
func (u user) isNull(t *testing.T, field, id string) {
	t.Helper()
	var out map[string]any
	u.must(t, `query($id: ID!) { `+field+`(id: $id) { id } }`, map[string]any{"id": id}, &out)
	if out[field] != nil {
		t.Fatalf("%s(%s) = %v, want null", field, id, out[field])
	}
}

func (u user) sees(t *testing.T, field, id string) {
	t.Helper()
	var out map[string]map[string]any
	u.must(t, `query($id: ID!) { `+field+`(id: $id) { id } }`, map[string]any{"id": id}, &out)
	if out[field]["id"] != id {
		t.Fatalf("%s(%s) = %v, want it visible", field, id, out[field])
	}
}

func contains(s, sub string) bool { return strings.Contains(strings.ToLower(s), strings.ToLower(sub)) }
