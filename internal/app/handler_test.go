package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bcc-media/mediabrain/internal/graph"
)

type pingFunc func(context.Context) error

func (f pingFunc) Ping(ctx context.Context) error { return f(ctx) }

var okPing = pingFunc(func(context.Context) error { return nil })

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	h := NewHandler(&graph.Resolver{}, okPing, HandlerOptions{})
	if rec := do(t, h, http.MethodGet, "/healthz", ""); rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("healthz = %d %q", rec.Code, rec.Body)
	}

	down := NewHandler(&graph.Resolver{}, pingFunc(func(context.Context) error { return errors.New("down") }), HandlerOptions{})
	if rec := do(t, down, http.MethodGet, "/healthz", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("healthz when db down = %d", rec.Code)
	}
}

func TestQueryEndpoint(t *testing.T) {
	h := NewHandler(&graph.Resolver{}, okPing, HandlerOptions{})
	rec := do(t, h, http.MethodPost, "/query", `{"query":"{ ping }"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"pong"`) {
		t.Fatalf("query = %d %s", rec.Code, rec.Body)
	}
}

func TestPlayground(t *testing.T) {
	on := NewHandler(&graph.Resolver{}, okPing, HandlerOptions{Playground: true})
	if rec := do(t, on, http.MethodGet, "/", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "MediaBrain") {
		t.Fatalf("playground on = %d", rec.Code)
	}
	off := NewHandler(&graph.Resolver{}, okPing, HandlerOptions{})
	if rec := do(t, off, http.MethodGet, "/", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("playground off = %d", rec.Code)
	}
}

func TestDebugMount(t *testing.T) {
	debug := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("debug:" + r.URL.Path)) })
	on := NewHandler(&graph.Resolver{}, okPing, HandlerOptions{Debug: debug})
	if rec := do(t, on, http.MethodGet, "/debug/api/stats", ""); rec.Body.String() != "debug:/api/stats" {
		t.Fatalf("debug mount = %d %q", rec.Code, rec.Body)
	}
	off := NewHandler(&graph.Resolver{}, okPing, HandlerOptions{})
	if rec := do(t, off, http.MethodGet, "/debug/", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("debug off = %d", rec.Code)
	}
}

func TestRequestLogging(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := logRequests(log, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/boom" {
			w.WriteHeader(http.StatusInternalServerError)
		}
		w.(http.Flusher).Flush()
	}))
	for _, p := range []string{"/x", "/healthz", "/boom"} {
		do(t, h, http.MethodGet, p, "")
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	want := []struct {
		level  string
		status float64
	}{{"INFO", 200}, {"DEBUG", 200}, {"ERROR", 500}}
	if len(lines) != len(want) {
		t.Fatalf("got %d log lines: %s", len(lines), buf.String())
	}
	for i, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatal(err)
		}
		if m["level"] != want[i].level || m["status"] != want[i].status || m["msg"] != "http request" {
			t.Errorf("line %d = %v", i, m)
		}
	}
}
