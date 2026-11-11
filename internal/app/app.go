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

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/asset"
	"github.com/bcc-media/mediabrain/internal/auth"
	"github.com/bcc-media/mediabrain/internal/config"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/devdata"
	"github.com/bcc-media/mediabrain/internal/file"
	"github.com/bcc-media/mediabrain/internal/graph"
	"github.com/bcc-media/mediabrain/internal/project"
	"github.com/bcc-media/mediabrain/internal/subtitle"
	"github.com/bcc-media/mediabrain/internal/vidispineimport"
)

const (
	shutdownTimeout = 10 * time.Second
	// sweepInterval bounds how long an expired grant keeps giving access to assets.
	sweepInterval = time.Minute
)

// App is a fully wired, listening application.
type App struct {
	pool   *pgxpool.Pool
	ln     net.Listener
	srv    *http.Server
	access *access.Service
}

// New connects to the database, optionally migrates, and binds the listener.
func New(ctx context.Context, cfg config.Config) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	proxies, err := auth.ParseProxies(cfg.AuthTrustedProxies)
	if err != nil {
		return nil, err
	}
	vidispineSrc, err := vidispineimport.NewSource(cfg.VidispineURL, cfg.VidispineUsername, cfg.VidispinePassword)
	if err != nil {
		return nil, fmt.Errorf("app: VIDISPINE_URL: %w", err)
	}
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

	q, tx := sqlc.New(pool), db.PoolTx(pool)
	accessSvc := access.NewService(q, tx)
	resolver := &graph.Resolver{
		AccessSvc:    accessSvc,
		ProjectsSvc:  project.NewService(q, tx),
		FilesSvc:     file.NewService(q, tx),
		AssetsSvc:    asset.NewService(q, tx),
		SubtitlesSvc: subtitle.NewService(q, tx),
		VidispineSvc: vidispineimport.NewService(q, tx, vidispineSrc),
	}
	opts := HandlerOptions{
		Playground: cfg.Playground,
		Auth:       auth.Options{Header: cfg.AuthHeader, TrustedProxies: proxies, Users: accessSvc, Debug: cfg.Debug, DebugUser: cfg.DebugUser},
	}
	if cfg.AuthConfigured() {
		opts.Auth.Verifier = &auth.JWKSVerifier{
			Issuer: cfg.AuthIssuer, Audience: cfg.AuthAudience, JWKSURL: cfg.AuthJWKSURL, GroupsClaim: cfg.AuthGroupsClaim,
		}
	}
	if cfg.Debug {
		slog.Warn("DEBUG enabled: /debug/ can wipe data and requests can act as any user — never enable in production")
		store := devdata.NewStore(pool)
		store.Vidispine = vidispineSrc
		opts.Debug = devdata.Handler(store)
	}
	srv := &http.Server{
		Handler:           NewHandler(resolver, pool, opts),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return &App{pool: pool, ln: ln, srv: srv, access: accessSvc}, nil
}

// Addr returns the address the server is listening on.
func (a *App) Addr() string { return a.ln.Addr().String() }

// Serve handles requests until ctx is cancelled, then shuts down gracefully.
func (a *App) Serve(ctx context.Context) error {
	defer a.pool.Close()

	errc := make(chan error, 1)
	go func() { errc <- a.srv.Serve(a.ln) }()
	slog.Info("listening", "addr", a.Addr())
	go a.sweep(ctx)

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

func (a *App) sweep(ctx context.Context) {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := a.access.Sweep(ctx); err != nil {
				slog.ErrorContext(ctx, "access: sweep expired grants", "error", err.Error())
			} else if n > 0 {
				slog.InfoContext(ctx, "access: removed expired grants", "count", n)
			}
		}
	}
}
