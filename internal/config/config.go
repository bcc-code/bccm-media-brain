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
	Playground  bool       // GRAPHQL_PLAYGROUND (default false): playground at / and schema introspection
	Debug       bool       // DEBUG (default false): enables /debug UI and dev-data API. Never in production.
	DebugUser   string     // DEBUG_USER: with DEBUG, requests that name no user act as this email (created as admin if missing)
	LogFormat   string     // LOG_FORMAT: "text" (default) or "json"
	LogLevel    slog.Level // LOG_LEVEL: debug|info|warn|error (default info)

	// Sign-in is done by a proxy in front of MediaBrain that forwards a signed JWT.
	AuthHeader         string // AUTH_HEADER (default "Authorization"): header carrying the JWT
	AuthTrustedProxies string // AUTH_TRUSTED_PROXIES: comma-separated IPs/CIDRs allowed to call /query
	AuthIssuer         string // AUTH_ISSUER: expected "iss"
	AuthAudience       string // AUTH_AUDIENCE: expected "aud"
	AuthJWKSURL        string // AUTH_JWKS_URL: where the proxy's signing keys are published
	AuthGroupsClaim    string // AUTH_GROUPS_CLAIM (default "groups"): claim listing SSO groups

	// Cantemo Portal (Vidispine), from which assets are imported. Optional as a group.
	CantemoURL      string // CANTEMO_URL: root of the Vidispine API, e.g. https://cantemo.example.com/API
	CantemoUsername string // CANTEMO_USERNAME
	CantemoPassword string // CANTEMO_PASSWORD
}

// ErrCantemoIncomplete is returned when CANTEMO_URL is set without credentials.
var ErrCantemoIncomplete = errors.New("config: CANTEMO_URL needs CANTEMO_USERNAME and CANTEMO_PASSWORD")

// ErrAuthNotConfigured is returned when the AUTH_* settings are missing outside debug mode.
var ErrAuthNotConfigured = errors.New("config: AUTH_ISSUER, AUTH_AUDIENCE, AUTH_JWKS_URL and AUTH_TRUSTED_PROXIES are required unless DEBUG=true")

// AuthConfigured reports whether tokens can be verified.
func (c Config) AuthConfigured() bool {
	return c.AuthIssuer != "" && c.AuthAudience != "" && c.AuthJWKSURL != "" && c.AuthTrustedProxies != ""
}

// Validate refuses to run without authentication unless debug mode provides the bypass.
func (c Config) Validate() error {
	if !c.Debug && !c.AuthConfigured() {
		return ErrAuthNotConfigured
	}
	return nil
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
	cfg := Config{HTTPAddr: ":8080", AutoMigrate: true, LogFormat: "text", AuthHeader: "Authorization", AuthGroupsClaim: "groups"}

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
	for key, dst := range map[string]*string{
		"DEBUG_USER":  &cfg.DebugUser,
		"AUTH_HEADER": &cfg.AuthHeader, "AUTH_TRUSTED_PROXIES": &cfg.AuthTrustedProxies,
		"AUTH_ISSUER": &cfg.AuthIssuer, "AUTH_AUDIENCE": &cfg.AuthAudience,
		"AUTH_JWKS_URL": &cfg.AuthJWKSURL, "AUTH_GROUPS_CLAIM": &cfg.AuthGroupsClaim,
		"CANTEMO_URL": &cfg.CantemoURL, "CANTEMO_USERNAME": &cfg.CantemoUsername, "CANTEMO_PASSWORD": &cfg.CantemoPassword,
	} {
		if v, ok := lookup(key); ok && v != "" {
			*dst = v
		}
	}
	if cfg.CantemoURL != "" && (cfg.CantemoUsername == "" || cfg.CantemoPassword == "") {
		return Config{}, ErrCantemoIncomplete
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
