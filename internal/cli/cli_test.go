package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bcc-media/mediabrain/internal/config"
)

// noConfig makes config.Load fail (no DATABASE_URL, no .env).
func noConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv(config.EnvFileVar, filepath.Join(t.TempDir(), "missing.env"))
}

// unreachableDB gives a valid config pointing at a closed port.
func unreachableDB(t *testing.T) {
	t.Setenv(config.EnvFileVar, filepath.Join(t.TempDir(), "missing.env"))
	t.Setenv("DATABASE_URL", "postgres://u:p@127.0.0.1:1/x?sslmode=disable&connect_timeout=1")
	t.Setenv("LOG_FORMAT", "text")
}

func TestHelpAndList(t *testing.T) {
	var out bytes.Buffer
	for _, h := range []string{"help", "-h", "--help"} {
		out.Reset()
		if err := Run(context.Background(), []string{h}, &out); err != nil || !strings.Contains(out.String(), "usage:") {
			t.Fatalf("%s: %v %q", h, err, out.String())
		}
	}
	out.Reset()
	if err := Run(context.Background(), []string{"seed", "list"}, &out); err != nil ||
		!strings.Contains(out.String(), "dummies") || !strings.Contains(out.String(), "count=<int>") {
		t.Fatalf("seed list: %v %q", err, out.String())
	}
}

func TestUsageErrors(t *testing.T) {
	unreachableDB(t)
	cases := [][]string{
		{"bogus"},
		{"migrate"},
		{"migrate", "sideways"},
		{"seed"},
		{"seed", "dummies", "-nope"},
		{"seed", "dummies", "notapair"},
	}
	for _, args := range cases {
		if err := Run(context.Background(), args, &bytes.Buffer{}); !errors.Is(err, ErrUsage) {
			t.Errorf("%v: err = %v, want ErrUsage", args, err)
		}
	}
}

func TestConfigErrors(t *testing.T) {
	noConfig(t)
	for _, args := range [][]string{nil, {"serve"}, {"migrate", "up"}, {"reset-data"}} {
		if err := Run(context.Background(), args, &bytes.Buffer{}); !errors.Is(err, config.ErrMissingDatabaseURL) {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	unreachableDB(t)
	t.Setenv("LOG_FORMAT", "xml")
	if err := Run(context.Background(), []string{"migrate", "up"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "logging") {
		t.Fatalf("bad log format: %v", err)
	}
}

func TestDBErrors(t *testing.T) {
	unreachableDB(t)
	for _, args := range [][]string{
		{"migrate", "up"}, {"migrate", "down"}, {"migrate", "status"}, {"migrate", "reset"},
		{"reset-data"}, {"seed", "dummies", "count=1"}, {"serve"},
	} {
		if err := Run(context.Background(), args, &bytes.Buffer{}); err == nil || errors.Is(err, ErrUsage) {
			t.Errorf("%v: err = %v, want connection error", args, err)
		}
	}
	if err := Run(context.Background(), []string{"seed", "nope"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected unknown scenario error")
	}
}
