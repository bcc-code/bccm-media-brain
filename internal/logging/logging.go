// Package logging configures the process-wide slog logger.
// All logging in this project goes through log/slog; set LOG_FORMAT=json for
// machine-readable output (e.g. for log shipping).
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// New returns a logger writing to w in the given format ("text" or "json").
func New(w io.Writer, format string, level slog.Level) (*slog.Logger, error) {
	opts := &slog.HandlerOptions{Level: level}
	switch strings.ToLower(format) {
	case "", "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("logging: unknown format %q (want text or json)", format)
	}
}
