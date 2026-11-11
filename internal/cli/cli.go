// Package cli implements the mediabrain command line.
//
//	mediabrain [serve]                         run the API server
//	mediabrain migrate up|down|status|reset    manage the schema
//	mediabrain seed list                       list dev-data scenarios
//	mediabrain seed <scenario> [flags] [k=v]   run a scenario
//	mediabrain reset-data                      truncate all tables
//	mediabrain admin grant|revoke <user>       make a user a system administrator, or not
//	mediabrain cantemo import [flags] <VX-id>  import one Cantemo item as an asset
//	mediabrain cantemo refresh <asset id>      refresh an imported asset's Cantemo metadata
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
	"github.com/bcc-media/mediabrain/internal/cantemo"
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
  mediabrain cantemo import [-type N] [-project PJ-...] <VX-id>
                                              import one Cantemo item as an asset (type 1 by default)
  mediabrain cantemo refresh <asset id>       replace an imported asset's Cantemo metadata
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
	case "serve", "migrate", "reset-data", "admin", "cantemo":
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
	case "cantemo":
		return cantemoCmd(ctx, cfg, args, out)
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

func cantemoCmd(ctx context.Context, cfg config.Config, args []string, out io.Writer) error {
	if len(args) == 0 || (args[0] != "import" && args[0] != "refresh") {
		return usageErr("cantemo needs import or refresh")
	}
	fs := flag.NewFlagSet("cantemo", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	typeID := fs.Int("type", 1, "asset type id")
	project := fs.String("project", "", "project to link the asset to")
	if err := fs.Parse(args[1:]); err != nil {
		return usageErr("%v", err)
	}
	if fs.NArg() != 1 {
		return usageErr("cantemo %s needs exactly one id", args[0])
	}
	id := fs.Arg(0)

	src, err := cantemo.NewSource(cfg.CantemoURL, cfg.CantemoUsername, cfg.CantemoPassword)
	if err != nil {
		return fmt.Errorf("CANTEMO_URL: %w", err)
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	svc := cantemo.NewService(sqlc.New(pool), db.PoolTx(pool), src)
	ctx = access.With(ctx, access.System())

	if args[0] == "refresh" {
		a, err := svc.Refresh(ctx, id)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: refreshed from %s\n", a.ID, cantemo.ItemID(a))
		return nil
	}
	in := cantemo.ImportInput{ItemID: id, TypeID: int16(*typeID)}
	if *project != "" {
		in.ProjectID = project
	}
	a, err := svc.Import(ctx, in)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: imported %s %q\n", a.ID, id, a.Title)
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
