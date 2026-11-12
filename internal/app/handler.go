package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql/playground"

	"github.com/bcc-media/mediabrain/internal/auth"
	"github.com/bcc-media/mediabrain/internal/config"
	"github.com/bcc-media/mediabrain/internal/graph"
)

// maxBody leaves room for a 5 MB subtitle file as a JSON string.
const maxBody = 8 << 20

// QueryTimeout is how long one /query request may run. Past it the request's context ends, so
// the statement in progress is cancelled, a transaction rolls back and the answer is TIMEOUT.
// Long enough for the largest import (a 5 MB subtitle file) on a slow day, short enough that a
// query nobody waits for any more does not keep the database busy.
const QueryTimeout = 30 * time.Second

// healthTimeout is how long /healthz waits for the database. A variable for tests.
var healthTimeout = 2 * time.Second

// writeGrace is how long after the deadline the answer may still be written.
const writeGrace = 15 * time.Second

// Pinger reports whether a dependency (e.g. the database) is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// HandlerOptions toggles optional routes.
type HandlerOptions struct {
	Playground bool         // serve the GraphQL playground at / and allow introspection
	Auth       auth.Options // how /query callers are authenticated
	Debug      http.Handler // if non-nil, mounted at /debug/ (dev only)
	// LocalOnly refuses requests whose Host is not localhost or a loopback IP. Debug mode on a
	// loopback listener sets it: without it, a site whose name is re-pointed at 127.0.0.1 (DNS
	// rebinding) would be same-origin with the server and could use the token-less sign-in.
	LocalOnly bool
	// QueryTimeout replaces the default of the same name. For tests.
	QueryTimeout time.Duration
}

// NewHandler builds the HTTP routes:
//
//	GET  /healthz  liveness + DB check
//	POST /query    GraphQL endpoint (authenticated)
//	GET  /         GraphQL playground (if enabled)
//	     /debug/   debug UI + dev-data API (if enabled)
func NewHandler(r *graph.Resolver, db Pinger, opts HandlerOptions) http.Handler {
	mux := http.NewServeMux()

	timeout := opts.QueryTimeout
	if timeout == 0 {
		timeout = QueryTimeout
	}
	gql := withDeadline(timeout, auth.Middleware(opts.Auth, graph.NewServer(r, opts.Playground)))
	mux.Handle("/query", http.MaxBytesHandler(gql, maxBody))

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, req *http.Request) {
		// The ping waits for a free pool connection. Under load that wait must end in an
		// answer, or the probe hangs until whoever asked gives up.
		ctx, cancel := context.WithTimeout(req.Context(), healthTimeout)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	if opts.Playground {
		mux.Handle("GET /{$}", playground.Handler("MediaBrain", "/query"))
	}
	if opts.Debug != nil {
		mux.Handle("/debug/", http.StripPrefix("/debug", opts.Debug))
	}
	return logRequests(slog.Default(), http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if opts.LocalOnly && !config.LoopbackHost(req.Host) {
			http.Error(w, "debug mode answers only to localhost; set DEBUG_ALLOW_REMOTE=true to use another host name", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, req)
	}))
}

// withDeadline ends the request's context after d. Everything below it, sign-in included, runs
// on that context.
func withDeadline(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), d)
		defer cancel()
		// Not a server-wide WriteTimeout: /debug/ runs scenarios that may take longer.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d + writeGrace))
		next.ServeHTTP(w, req.WithContext(ctx))
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			slog.WarnContext(req.Context(), "graphql: request stopped at its deadline", "deadline", d.String(), "remote", req.RemoteAddr)
		}
	})
}
