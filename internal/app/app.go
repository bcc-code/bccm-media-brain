// Package app wires configuration, database, services and the HTTP server.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bcc-media/mediabrain/internal/config"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/devdata"
	"github.com/bcc-media/mediabrain/internal/dummy"
	"github.com/bcc-media/mediabrain/internal/graph"
)

const shutdownTimeout = 10 * time.Second

// App is a fully wired, listening application.
type App struct {
	pool *pgxpool.Pool
	ln   net.Listener
	srv  *http.Server
}

// New connects to the database, optionally migrates, and binds the listener.
func New(ctx context.Context, cfg config.Config) (*App, error) {
	if cfg.AutoMigrate {
		if err := db.MigrateUp(ctx, cfg.DatabaseURL); err != nil {
			return nil, err
		}
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("app: listen %s: %w", cfg.HTTPAddr, err)
	}

	resolver := &graph.Resolver{DummySvc: dummy.NewService(sqlc.New(pool))}
	opts := HandlerOptions{Playground: cfg.Playground}
	if cfg.Debug {
		slog.Warn("DEBUG enabled: /debug/ can wipe and generate data — never enable in production")
		opts.Debug = devdata.Handler(devdata.NewStore(pool))
	}
	srv := &http.Server{
		Handler:           NewHandler(resolver, pool, opts),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return &App{pool: pool, ln: ln, srv: srv}, nil
}

// Addr returns the address the server is listening on.
func (a *App) Addr() string { return a.ln.Addr().String() }

// Serve handles requests until ctx is cancelled, then shuts down gracefully.
func (a *App) Serve(ctx context.Context) error {
	defer a.pool.Close()

	errc := make(chan error, 1)
	go func() { errc <- a.srv.Serve(a.ln) }()
	slog.Info("listening", "addr", a.Addr())

	select {
	case err := <-errc:
		return fmt.Errorf("app: serve: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := a.srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("app: shutdown: %w", err)
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("app: serve: %w", err)
	}
	return nil
}

// Run is New followed by Serve.
func Run(ctx context.Context, cfg config.Config) error {
	a, err := New(ctx, cfg)
	if err != nil {
		return err
	}
	return a.Serve(ctx)
}
