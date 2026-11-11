// Package cli implements the mediabrain command line.
//
//	mediabrain [serve]                         run the API server
//	mediabrain migrate up|down|status|reset    manage the schema
//	mediabrain seed list                       list dev-data scenarios
//	mediabrain seed <scenario> [flags] [k=v]   run a scenario
//	mediabrain reset-data                      truncate all tables
//	mediabrain admin grant|revoke <user>       make a user a system administrator, or not
//	mediabrain service create|token|revoke|list  manage machine clients and their tokens
//	mediabrain vidispine import [flags] <VX-id>  import one Vidispine item as an asset
//	mediabrain vidispine refresh <asset id>      refresh an imported asset's Vidispine metadata
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
	"strconv"
	"strings"
	"time"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/app"
	"github.com/bcc-media/mediabrain/internal/config"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/devdata"
	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/internal/logging"
	"github.com/bcc-media/mediabrain/internal/vidispineimport"
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
  mediabrain service create [-admin] <name>   add a service account (a machine client)
  mediabrain service token [-name label] [-expires 365d] <account>
                                              create a token for it; the token is printed once
  mediabrain service revoke <token id>        revoke one token
  mediabrain service list                     list service accounts and their tokens
  mediabrain vidispine import [-type N] [-project PJ-...] <VX-id>
                                              import one Vidispine item as an asset (type 1 by default)
  mediabrain vidispine refresh <asset id>       replace an imported asset's Vidispine metadata
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
	case "serve", "migrate", "reset-data", "admin", "service", "vidispine":
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
	case "service":
		return service(ctx, cfg, args, out)
	case "vidispine":
		return vidispineCmd(ctx, cfg, args, out)
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

// lifetime reads a duration that may also be given in days ("90d").
func lifetime(s string) (time.Duration, error) {
	var d time.Duration
	var err error
	if days, ok := strings.CutSuffix(s, "d"); ok {
		var n int
		n, err = strconv.Atoi(days)
		d = time.Duration(n) * 24 * time.Hour
	} else {
		d, err = time.ParseDuration(s)
	}
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q is not a lifetime such as 90d or 12h", s)
	}
	return d, nil
}

func service(ctx context.Context, cfg config.Config, args []string, out io.Writer) error {
	if len(args) == 0 {
		return usageErr("service needs create, token, revoke or list")
	}
	action := args[0]
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	admin := fs.Bool("admin", false, "make the account a system administrator")
	label := fs.String("name", "cli", "what the token is for")
	expires := fs.String("expires", "365d", "lifetime of the token")
	if err := fs.Parse(args[1:]); err != nil {
		return usageErr("%v", err)
	}
	want := map[string]int{"create": 1, "token": 1, "revoke": 1, "list": 0}
	n, ok := want[action]
	if !ok {
		return usageErr("unknown service action %q", action)
	}
	if fs.NArg() != n {
		return usageErr("service %s needs %d argument(s)", action, n)
	}
	ttl, err := lifetime(*expires)
	if err != nil {
		return usageErr("-expires: %v", err)
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	svc := access.NewService(sqlc.New(pool), db.PoolTx(pool))
	ctx = access.With(ctx, access.System())

	switch action {
	case "create":
		u, err := svc.CreateServiceAccount(ctx, access.ServiceAccountInput{Name: fs.Arg(0), Admin: *admin})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: service account %q (administrator = %v)\n", u.ID, fs.Arg(0), u.IsAdmin)
	case "token":
		at := time.Now().Add(ttl)
		t, err := svc.CreateServiceToken(ctx, access.ServiceTokenInput{ServiceAccount: fs.Arg(0), Name: *label, ExpiresAt: &at})
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("no service account %q: create it with `service create`", fs.Arg(0))
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: token %q, expires %s. It is not shown again:\n%s\n",
			t.ServiceToken.ID, t.ServiceToken.Name, t.ServiceToken.ExpiresAt.Format(time.DateOnly), t.Token)
	case "revoke":
		t, err := svc.RevokeServiceToken(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: revoked\n", t.ID)
	default:
		accounts, err := svc.ServiceAccounts(ctx)
		if err != nil {
			return err
		}
		for _, u := range accounts {
			fmt.Fprintf(out, "%s %s (administrator = %v, active = %v)\n", u.ID, *u.Name, u.IsAdmin, u.Active)
			tokens, err := svc.ServiceTokens(ctx, u.ID)
			if err != nil {
				return err
			}
			for _, t := range tokens {
				state := "expires " + t.ExpiresAt.Format(time.DateOnly)
				if t.RevokedAt != nil {
					state = "revoked " + t.RevokedAt.Format(time.DateOnly)
				}
				fmt.Fprintf(out, "  %s %q %s\n", t.ID, t.Name, state)
			}
		}
	}
	return nil
}

func vidispineCmd(ctx context.Context, cfg config.Config, args []string, out io.Writer) error {
	if len(args) == 0 || (args[0] != "import" && args[0] != "refresh") {
		return usageErr("vidispine needs import or refresh")
	}
	fs := flag.NewFlagSet("vidispine", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	typeID := fs.Int("type", 1, "asset type id")
	project := fs.String("project", "", "project to link the asset to")
	if err := fs.Parse(args[1:]); err != nil {
		return usageErr("%v", err)
	}
	if fs.NArg() != 1 {
		return usageErr("vidispine %s needs exactly one id", args[0])
	}
	id := fs.Arg(0)

	src, err := vidispineimport.NewSource(cfg.VidispineURL, cfg.VidispineUsername, cfg.VidispinePassword)
	if err != nil {
		return fmt.Errorf("VIDISPINE_URL: %w", err)
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	svc := vidispineimport.NewService(sqlc.New(pool), db.PoolTx(pool), src)
	ctx = access.With(ctx, access.System())

	if args[0] == "refresh" {
		a, err := svc.Refresh(ctx, id)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: refreshed from %s\n", a.ID, vidispineimport.ItemID(a))
		return nil
	}
	in := vidispineimport.ImportInput{ItemID: id, TypeID: int16(*typeID)}
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
