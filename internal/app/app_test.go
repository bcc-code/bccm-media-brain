package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bcc-media/mediabrain/internal/config"
)

// unreachable points at a closed port so connection attempts fail fast.
const unreachable = "postgres://u:p@127.0.0.1:1/x?sslmode=disable&connect_timeout=1"

func TestNewFailsWithoutDatabase(t *testing.T) {
	for _, migrate := range []bool{true, false} {
		cfg := config.Config{DatabaseURL: unreachable, HTTPAddr: "127.0.0.1:0", AutoMigrate: migrate, Debug: true}
		if _, err := New(context.Background(), cfg); err == nil {
			t.Fatalf("AutoMigrate=%v: expected error", migrate)
		}
		if err := Run(context.Background(), cfg); err == nil {
			t.Fatalf("Run AutoMigrate=%v: expected error", migrate)
		}
	}
}

func TestNewRefusesToRunWithoutAuth(t *testing.T) {
	cfg := config.Config{DatabaseURL: unreachable, HTTPAddr: "127.0.0.1:0"}
	if _, err := New(context.Background(), cfg); !errors.Is(err, config.ErrAuthNotConfigured) {
		t.Fatalf("err = %v", err)
	}
	cfg.Debug, cfg.AuthTrustedProxies = true, "not-an-ip"
	if _, err := New(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "trusted proxy") {
		t.Fatalf("bad proxies: %v", err)
	}
}
