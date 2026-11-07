// Command mediabrain runs the MediaBrain API server and its dev/ops subcommands.
// See internal/cli for the command list (`mediabrain help`).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bcc-media/mediabrain/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.Run(ctx, os.Args[1:], os.Stdout); err != nil {
		if errors.Is(err, cli.ErrUsage) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}
