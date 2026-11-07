package app

import (
	"context"
	"testing"

	"github.com/bcc-media/mediabrain/internal/config"
)

// unreachable points at a closed port so connection attempts fail fast.
const unreachable = "postgres://u:p@127.0.0.1:1/x?sslmode=disable&connect_timeout=1"

func TestNewFailsWithoutDatabase(t *testing.T) {
	for _, migrate := range []bool{true, false} {
		cfg := config.Config{DatabaseURL: unreachable, HTTPAddr: "127.0.0.1:0", AutoMigrate: migrate}
		if _, err := New(context.Background(), cfg); err == nil {
			t.Fatalf("AutoMigrate=%v: expected error", migrate)
		}
		if err := Run(context.Background(), cfg); err == nil {
			t.Fatalf("Run AutoMigrate=%v: expected error", migrate)
		}
	}
}
