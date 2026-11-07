//go:build e2e

// Package e2e runs the full application against a real Postgres in docker.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bcc-media/mediabrain/internal/app"
	"github.com/bcc-media/mediabrain/internal/config"
)

var (
	dbURL   string // connection string of the test container
	baseURL string // http://host:port of the running app
)

func TestMain(m *testing.M) { os.Exit(run(m)) }

func run(m *testing.M) int {
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
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

	a, err := app.New(ctx, config.Config{DatabaseURL: dbURL, HTTPAddr: "127.0.0.1:0", AutoMigrate: true, Playground: true, Debug: true})
	if err != nil {
		log.Printf("start app: %v", err)
		return 1
	}
	appCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Serve(appCtx) }()
	baseURL = "http://" + a.Addr()

	code := m.Run()

	stop()
	if err := <-done; err != nil {
		log.Printf("app shutdown: %v", err)
		return 1
	}
	return code
}

type gqlError struct {
	Message string `json:"message"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
}

// gql posts a GraphQL request and decodes data into out (if non-nil).
func gql(t *testing.T, query string, vars map[string]any, out any) []gqlError {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/query", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /query: %v", err)
	}
	defer res.Body.Close()

	var r gqlResponse
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		t.Fatalf("decode response (status %d): %v", res.StatusCode, err)
	}
	if out != nil && len(r.Data) > 0 && string(r.Data) != "null" {
		if err := json.Unmarshal(r.Data, out); err != nil {
			t.Fatalf("decode data %s: %v", r.Data, err)
		}
	}
	return r.Errors
}

// mustGQL is gql that fails the test on GraphQL errors.
func mustGQL(t *testing.T, query string, vars map[string]any, out any) {
	t.Helper()
	if errs := gql(t, query, vars, out); len(errs) > 0 {
		t.Fatalf("graphql errors: %v", fmt.Sprint(errs))
	}
}
