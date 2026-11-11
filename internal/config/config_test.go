package config

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoadFrom(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr error
		anyErr  bool
	}{
		{name: "missing url", env: map[string]string{}, wantErr: ErrMissingDatabaseURL},
		{name: "empty url", env: map[string]string{"DATABASE_URL": ""}, wantErr: ErrMissingDatabaseURL},
		{
			name: "defaults",
			env:  map[string]string{"DATABASE_URL": "postgres://x"},
			want: Config{DatabaseURL: "postgres://x", HTTPAddr: ":8080", AutoMigrate: true, LogFormat: "text", AuthHeader: "Authorization", AuthGroupsClaim: "groups"},
		},
		{
			name: "overrides",
			env: map[string]string{
				"DATABASE_URL": "postgres://x", "HTTP_ADDR": ":9000",
				"AUTO_MIGRATE": "false", "GRAPHQL_PLAYGROUND": "1", "DEBUG": "true", "DEBUG_USER": "dev@example.test",
				"LOG_FORMAT": "json", "LOG_LEVEL": "debug",
				"AUTH_HEADER": "X-Token", "AUTH_TRUSTED_PROXIES": "10.0.0.0/8", "AUTH_ISSUER": "https://idp",
				"AUTH_AUDIENCE": "mb", "AUTH_JWKS_URL": "https://idp/keys", "AUTH_GROUPS_CLAIM": "roles",
			},
			want: Config{
				DatabaseURL: "postgres://x", HTTPAddr: ":9000", Debug: true, DebugUser: "dev@example.test", Playground: true, LogFormat: "json", LogLevel: slog.LevelDebug,
				AuthHeader: "X-Token", AuthTrustedProxies: "10.0.0.0/8", AuthIssuer: "https://idp",
				AuthAudience: "mb", AuthJWKSURL: "https://idp/keys", AuthGroupsClaim: "roles",
			},
		},
		{name: "bad auto migrate", env: map[string]string{"DATABASE_URL": "x", "AUTO_MIGRATE": "nope"}, anyErr: true},
		{name: "bad log level", env: map[string]string{"DATABASE_URL": "x", "LOG_LEVEL": "loud"}, anyErr: true},
		{name: "bad debug", env: map[string]string{"DATABASE_URL": "x", "DEBUG": "nope"}, anyErr: true},
		{name: "bad playground", env: map[string]string{"DATABASE_URL": "x", "GRAPHQL_PLAYGROUND": "nope"}, anyErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadFrom(env(tt.env))
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			case tt.anyErr:
				if err == nil {
					t.Fatal("expected error")
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if got != tt.want {
					t.Fatalf("got %+v, want %+v", got, tt.want)
				}
			}
		})
	}
}

func TestValidate(t *testing.T) {
	full := Config{AuthIssuer: "i", AuthAudience: "a", AuthJWKSURL: "j", AuthTrustedProxies: "127.0.0.1"}
	if err := full.Validate(); err != nil {
		t.Fatalf("configured auth: %v", err)
	}
	if err := (Config{Debug: true}).Validate(); err != nil {
		t.Fatalf("debug without auth: %v", err)
	}
	partial := full
	partial.AuthTrustedProxies = ""
	for _, c := range []Config{{}, partial} {
		if err := c.Validate(); !errors.Is(err, ErrAuthNotConfigured) {
			t.Fatalf("%+v: err = %v", c, err)
		}
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Chdir(t.TempDir()) // no .env here
	t.Setenv("DATABASE_URL", "postgres://env")
	cfg, err := Load()
	if err != nil || cfg.DatabaseURL != "postgres://env" {
		t.Fatalf("Load() = %+v, %v", cfg, err)
	}
}

func TestLoadDotenvFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, filepath.Join(dir, ".env"), "DATABASE_URL=postgres://file\nHTTP_ADDR=:7000\n")

	// File provides values when env is unset...
	t.Setenv("DATABASE_URL", "") // registers restore on cleanup
	t.Setenv("HTTP_ADDR", "")
	os.Unsetenv("DATABASE_URL")
	os.Unsetenv("HTTP_ADDR")
	cfg, err := Load()
	if err != nil || cfg.DatabaseURL != "postgres://file" || cfg.HTTPAddr != ":7000" {
		t.Fatalf("Load() = %+v, %v", cfg, err)
	}
	// ...but real env vars win.
	t.Setenv("HTTP_ADDR", ":7001")
	if cfg, _ := Load(); cfg.HTTPAddr != ":7001" {
		t.Fatalf("env should override file, got %q", cfg.HTTPAddr)
	}
}

func TestLoadCustomEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.env")
	writeFile(t, path, "DATABASE_URL=postgres://custom\n")
	t.Setenv("DATABASE_URL", "")
	os.Unsetenv("DATABASE_URL")
	t.Setenv(EnvFileVar, path)
	if cfg, err := Load(); err != nil || cfg.DatabaseURL != "postgres://custom" {
		t.Fatalf("Load() = %+v, %v", cfg, err)
	}
}

func TestLoadBadEnvFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvFileVar, filepath.Join(dir, "bad.env"))
	writeFile(t, filepath.Join(dir, "bad.env"), "not a pair\n")
	if _, err := Load(); err == nil {
		t.Fatal("expected parse error")
	}
	t.Setenv(EnvFileVar, dir) // a directory: open succeeds, read fails
	if _, err := Load(); err == nil {
		t.Fatal("expected read error")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCantemoSettings(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x"}
	with := func(extra map[string]string) func(string) (string, bool) {
		all := map[string]string{}
		for _, m := range []map[string]string{base, extra} {
			for k, v := range m {
				all[k] = v
			}
		}
		return env(all)
	}

	cfg, err := LoadFrom(with(nil))
	if err != nil || cfg.CantemoURL != "" {
		t.Fatalf("unset: %+v, %v", cfg, err)
	}
	cfg, err = LoadFrom(with(map[string]string{"CANTEMO_URL": "https://c.example/API", "CANTEMO_USERNAME": "u", "CANTEMO_PASSWORD": "p"}))
	if err != nil || cfg.CantemoURL != "https://c.example/API" || cfg.CantemoUsername != "u" || cfg.CantemoPassword != "p" {
		t.Fatalf("set: %+v, %v", cfg, err)
	}
	for _, partial := range []map[string]string{
		{"CANTEMO_URL": "https://c.example/API"},
		{"CANTEMO_URL": "https://c.example/API", "CANTEMO_USERNAME": "u"},
		{"CANTEMO_URL": "https://c.example/API", "CANTEMO_PASSWORD": "p"},
	} {
		if _, err := LoadFrom(with(partial)); !errors.Is(err, ErrCantemoIncomplete) {
			t.Errorf("%v: err = %v", partial, err)
		}
	}
	// Credentials without a URL configure nothing.
	if _, err := LoadFrom(with(map[string]string{"CANTEMO_USERNAME": "u"})); err != nil {
		t.Fatal(err)
	}
}
