package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"

	"github.com/bcc-media/mediabrain/internal/graph"
	"github.com/bcc-media/mediabrain/internal/graph/generated"
)

// Pinger reports whether a dependency (e.g. the database) is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// HandlerOptions toggles optional routes.
type HandlerOptions struct {
	Playground bool         // serve the GraphQL playground at /
	Debug      http.Handler // if non-nil, mounted at /debug/ (dev only)
}

// NewHandler builds the HTTP routes:
//
//	GET  /healthz  liveness + DB check
//	POST /query    GraphQL endpoint
//	GET  /         GraphQL playground (if enabled)
//	     /debug/   debug UI + dev-data API (if enabled)
func NewHandler(r *graph.Resolver, db Pinger, opts HandlerOptions) http.Handler {
	mux := http.NewServeMux()

	gql := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{Resolvers: r}))
	mux.Handle("/query", gql)

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
	return logRequests(slog.Default(), mux)
}
