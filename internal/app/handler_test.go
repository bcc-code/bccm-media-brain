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
	"time"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/auth"
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

type anyUser struct{}

func (anyUser) SignIn(context.Context, access.Identity) (access.Principal, error) {
	return access.Principal{}, errors.New("unused")
}

func (anyUser) ServiceSignIn(context.Context, string) (access.Principal, time.Time, error) {
	return access.Principal{}, time.Time{}, errors.New("unused")
}

func (anyUser) Impersonate(_ context.Context, ref string) (access.Principal, error) {
	return access.Principal{UserID: ref}, nil
}

func (anyUser) DebugAdmin(_ context.Context, email string) (access.Principal, error) {
	return access.Principal{UserID: email, Admin: true}, nil
}

// query posts a GraphQL request as the debug user "US-1", or anonymously.
func query(h http.Handler, body string, authenticated bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authenticated {
		req.Header.Set(auth.DebugUserHeader, "US-1")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestQueryEndpoint(t *testing.T) {
	opts := HandlerOptions{Auth: auth.Options{Users: anyUser{}, Debug: true}}
	h := NewHandler(&graph.Resolver{}, okPing, opts)

	rec := query(h, `{"query":"{ ping }"}`, true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"pong"`) {
		t.Fatalf("query = %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff header")
	}
	if rec := query(h, `{"query":"{ ping }"}`, false); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "UNAUTHENTICATED") {
		t.Fatalf("anonymous = %d %s", rec.Code, rec.Body)
	}
	if rec := query(h, `{"query":"{ __schema { types { name } } }"}`, true); !strings.Contains(rec.Body.String(), "introspection disabled") {
		t.Fatalf("introspection without playground = %s", rec.Body)
	}
	huge := `{"query":"{ ping }","variables":{"x":"` + strings.Repeat("a", maxBody) + `"}}`
	if rec := query(h, huge, true); strings.Contains(rec.Body.String(), `"pong"`) {
		t.Fatalf("oversized body was accepted: %d", rec.Code)
	}
	deep := "{" + strings.Repeat(" a: ping", graph.ComplexityLimit+1) + " }"
	if rec := query(h, `{"query":"`+deep+`"}`, true); !strings.Contains(rec.Body.String(), "complexity") {
		t.Fatalf("complexity limit = %s", rec.Body.String()[:200])
	}

	opts.Playground = true
	open := NewHandler(&graph.Resolver{}, okPing, opts)
	if rec := query(open, `{"query":"{ __schema { queryType { name } } }"}`, true); !strings.Contains(rec.Body.String(), `"Query"`) {
		t.Fatalf("introspection with playground = %s", rec.Body)
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

func TestLocalOnly(t *testing.T) {
	h := NewHandler(&graph.Resolver{}, okPing, HandlerOptions{LocalOnly: true})
	for host, code := range map[string]int{
		"localhost:8080": 200, "127.0.0.1:8080": 200, "[::1]:8080": 200, "localhost": 200,
		"attacker.example:8080": 403, "192.168.1.5:8080": 403, "": 403,
	} {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != code {
			t.Errorf("Host %q = %d, want %d", host, rec.Code, code)
		}
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
