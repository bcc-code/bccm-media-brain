// Package cli implements the mediabrain command line.
//
//	mediabrain [serve]                         run the API server
//	mediabrain migrate up|down|status|reset    manage the schema
//	mediabrain seed list                       list dev-data scenarios
//	mediabrain seed <scenario> [flags] [k=v]   run a scenario
//	mediabrain reset-data                      truncate all tables
//	mediabrain admin grant|revoke <user>       make a user a system administrator, or not
//
// Configuration comes from the environment and .env (see internal/config).
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/app"
	"github.com/bcc-media/mediabrain/internal/config"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/devdata"
	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/internal/logging"
)

// ErrUsage is returned for invalid command lines.
var ErrUsage = errors.New("usage error")

const usage = `usage:
  mediabrain [serve]                          run the API server
  mediabrain migrate up|down|status|reset     manage the database schema
  mediabrain seed list                        list dev-data scenarios
  mediabrain seed <scenario> [-reset] [-seed N] [param=value ...]
  mediabrain reset-data                       truncate all application tables
  mediabrain admin grant|revoke <email|id>    make a signed-in user a system administrator, or not
`

// Run executes the command line args (without the program name).
func Run(ctx context.Context, args []string, out io.Writer) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return nil
	case "seed":
		if len(args) == 1 && args[0] == "list" {
			listScenarios(out)
			return nil
		}
	case "serve", "migrate", "reset-data", "admin":
	default:
		return usageErr("unknown command %q", cmd)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger, err := logging.New(os.Stderr, cfg.LogFormat, cfg.LogLevel)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)

	switch cmd {
	case "serve":
		return app.Run(ctx, cfg)
	case "migrate":
		return migrate(ctx, cfg, args, out)
	case "seed":
		return seed(ctx, cfg, args, out)
	case "admin":
		return admin(ctx, cfg, args, out)
	default: // reset-data
		return withStore(ctx, cfg, func(s *devdata.Store) error {
			if err := s.Reset(ctx); err != nil {
				return err
			}
			fmt.Fprintln(out, "all data reset")
			return nil
		})
	}
}

func usageErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s\n\n%s", ErrUsage, fmt.Sprintf(format, a...), usage)
}

func migrate(ctx context.Context, cfg config.Config, args []string, out io.Writer) error {
	if len(args) != 1 {
		return usageErr("migrate needs exactly one of up|down|status|reset")
	}
	switch args[0] {
	case "up":
		return db.MigrateUp(ctx, cfg.DatabaseURL)
	case "down":
		return db.MigrateDown(ctx, cfg.DatabaseURL)
	case "status":
		return db.MigrateStatus(ctx, cfg.DatabaseURL, out)
	case "reset":
		if err := db.MigrateReset(ctx, cfg.DatabaseURL); err != nil {
			return err
		}
		return db.MigrateUp(ctx, cfg.DatabaseURL)
	default:
		return usageErr("unknown migrate action %q", args[0])
	}
}

func listScenarios(out io.Writer) {
	for _, s := range devdata.Scenarios() {
		fmt.Fprintf(out, "%-20s %s\n", s.Name, s.Description)
		for _, p := range s.Params {
			fmt.Fprintf(out, "%22s%s=<%s> (default %q) %s\n", "", p.Name, p.Type, p.Default, p.Help)
		}
	}
}

func seed(ctx context.Context, cfg config.Config, args []string, out io.Writer) error {
	if len(args) == 0 {
		return usageErr("seed needs a scenario name (try `seed list`)")
	}
	name := args[0]
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	reset := fs.Bool("reset", false, "wipe all data first")
	seedVal := fs.Uint64("seed", 0, "random seed (0 = random)")
	if err := fs.Parse(args[1:]); err != nil {
		return usageErr("%v", err)
	}
	params := map[string]string{}
	for _, kv := range fs.Args() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return usageErr("expected param=value, got %q", kv)
		}
		params[k] = v
	}
	if _, err := devdata.Find(name); err != nil {
		return err
	}
	return withStore(ctx, cfg, func(s *devdata.Store) error {
		res, err := s.Apply(ctx, name, devdata.ApplyRequest{Reset: *reset, Seed: *seedVal, Params: params})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: %s (seed %d, reset %v, %s)\n", res.Scenario, res.Summary, res.Seed, res.Reset, res.Duration.Round(1e6))
		return nil
	})
}

func admin(ctx context.Context, cfg config.Config, args []string, out io.Writer) error {
	if len(args) != 2 || (args[0] != "grant" && args[0] != "revoke") {
		return usageErr("admin needs grant|revoke and a user email or id")
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	svc := access.NewService(sqlc.New(pool), db.PoolTx(pool))
	u, err := svc.SetAdmin(access.With(ctx, access.System()), args[1], args[0] == "grant")
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("no user %q: they must sign in once before they can be made an administrator", args[1])
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: administrator = %v\n", u.ID, u.IsAdmin)
	return nil
}

func withStore(ctx context.Context, cfg config.Config, fn func(*devdata.Store) error) error {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return fn(devdata.NewStore(pool))
}
