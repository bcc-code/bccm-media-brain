package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/99designs/gqlgen/graphql/playground"

	"github.com/bcc-media/mediabrain/internal/auth"
	"github.com/bcc-media/mediabrain/internal/config"
	"github.com/bcc-media/mediabrain/internal/graph"
)

// maxBody leaves room for a 5 MB subtitle file as a JSON string.
const maxBody = 8 << 20

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
}

// NewHandler builds the HTTP routes:
//
//	GET  /healthz  liveness + DB check
//	POST /query    GraphQL endpoint (authenticated)
//	GET  /         GraphQL playground (if enabled)
//	     /debug/   debug UI + dev-data API (if enabled)
func NewHandler(r *graph.Resolver, db Pinger, opts HandlerOptions) http.Handler {
	mux := http.NewServeMux()

	gql := auth.Middleware(opts.Auth, graph.NewServer(r, opts.Playground))
	mux.Handle("/query", http.MaxBytesHandler(gql, maxBody))

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, req *http.Request) {
		if err := db.Ping(req.Context()); err != nil {
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
