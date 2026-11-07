// Package config loads runtime configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
)

// Config is the application configuration.
type Config struct {
	DatabaseURL string     // DATABASE_URL (required)
	HTTPAddr    string     // HTTP_ADDR (default ":8080")
	AutoMigrate bool       // AUTO_MIGRATE (default true)
	Playground  bool       // GRAPHQL_PLAYGROUND (default true)
	Debug       bool       // DEBUG (default false): enables /debug UI and dev-data API. Never in production.
	LogFormat   string     // LOG_FORMAT: "text" (default) or "json"
	LogLevel    slog.Level // LOG_LEVEL: debug|info|warn|error (default info)
}

// ErrMissingDatabaseURL is returned when DATABASE_URL is not set.
var ErrMissingDatabaseURL = errors.New("config: DATABASE_URL is required")

// Load reads configuration from the process environment, falling back to a
// dotenv file (".env" in the working directory, or $MEDIABRAIN_ENV_FILE).
// Real environment variables always win over the file.
func Load() (Config, error) {
	path := ".env"
	if p, ok := os.LookupEnv(EnvFileVar); ok && p != "" {
		path = p
	}
	lookup, err := dotenvLookup(os.LookupEnv, path)
	if err != nil {
		return Config{}, err
	}
	return LoadFrom(lookup)
}

// LoadFrom reads configuration using the given lookup function.
func LoadFrom(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{HTTPAddr: ":8080", AutoMigrate: true, Playground: true, LogFormat: "text"}

	url, ok := lookup("DATABASE_URL")
	if !ok || url == "" {
		return Config{}, ErrMissingDatabaseURL
	}
	cfg.DatabaseURL = url

	if v, ok := lookup("HTTP_ADDR"); ok && v != "" {
		cfg.HTTPAddr = v
	}
	var err error
	if cfg.AutoMigrate, err = boolEnv(lookup, "AUTO_MIGRATE", cfg.AutoMigrate); err != nil {
		return Config{}, err
	}
	if cfg.Playground, err = boolEnv(lookup, "GRAPHQL_PLAYGROUND", cfg.Playground); err != nil {
		return Config{}, err
	}
	if cfg.Debug, err = boolEnv(lookup, "DEBUG", cfg.Debug); err != nil {
		return Config{}, err
	}
	if v, ok := lookup("LOG_FORMAT"); ok && v != "" {
		cfg.LogFormat = v
	}
	if v, ok := lookup("LOG_LEVEL"); ok && v != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(v)); err != nil {
			return Config{}, fmt.Errorf("config: LOG_LEVEL: %w", err)
		}
	}
	return cfg, nil
}

func boolEnv(lookup func(string) (string, bool), key string, def bool) (bool, error) {
	v, ok := lookup(key)
	if !ok || v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("config: %s: %w", key, err)
	}
	return b, nil
}
