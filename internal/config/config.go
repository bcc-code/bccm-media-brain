// Package config loads runtime configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
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

	DebugAllowRemote bool // DEBUG_ALLOW_REMOTE (default false): with DEBUG, HTTP_ADDR may be reachable from other machines

	// Sign-in is done by a proxy in front of MediaBrain that forwards a signed JWT.
	AuthHeader         string // AUTH_HEADER (default "Authorization"): header carrying the JWT
	AuthServiceHeader  string // AUTH_SERVICE_HEADER (default "Authorization"): header carrying a machine client's service token
	AuthTrustedProxies string // AUTH_TRUSTED_PROXIES: comma-separated IPs/CIDRs allowed to call /query
	AuthIssuer         string // AUTH_ISSUER: expected "iss"
	AuthAudience       string // AUTH_AUDIENCE: expected "aud"
	AuthJWKSURL        string // AUTH_JWKS_URL: where the proxy's signing keys are published
	AuthGroupsClaim    string // AUTH_GROUPS_CLAIM (default "groups"): claim listing SSO groups

	// Vidispine, from which assets are imported. Optional as a group.
	VidispineURL      string // VIDISPINE_URL: root of the Vidispine API, e.g. https://vidispine.example.com/API
	VidispineUsername string // VIDISPINE_USERNAME
	VidispinePassword string // VIDISPINE_PASSWORD
}

// ErrVidispineIncomplete is returned when VIDISPINE_URL is set without credentials.
var ErrVidispineIncomplete = errors.New("config: VIDISPINE_URL needs VIDISPINE_USERNAME and VIDISPINE_PASSWORD")

// ErrAuthNotConfigured is returned when the AUTH_* settings are missing outside debug mode.
var ErrAuthNotConfigured = errors.New("config: AUTH_ISSUER, AUTH_AUDIENCE, AUTH_JWKS_URL and AUTH_TRUSTED_PROXIES are required unless DEBUG=true")

// AuthConfigured reports whether tokens can be verified.
func (c Config) AuthConfigured() bool {
	return c.AuthIssuer != "" && c.AuthAudience != "" && c.AuthJWKSURL != "" && c.AuthTrustedProxies != ""
}

// ErrDebugNotLocal is returned when debug mode would be reachable from other machines.
var ErrDebugNotLocal = errors.New("config: DEBUG=true needs HTTP_ADDR on a loopback address (for example 127.0.0.1:8080); set DEBUG_ALLOW_REMOTE=true to expose the debug API and the token-less sign-in to the network")

// Validate refuses to run without authentication unless debug mode provides the bypass, and
// refuses to offer that bypass to other machines unless that was asked for.
func (c Config) Validate() error {
	if !c.Debug && !c.AuthConfigured() {
		return ErrAuthNotConfigured
	}
	if c.Debug && !c.DebugAllowRemote && !LoopbackHost(c.HTTPAddr) {
		return ErrDebugNotLocal
	}
	return nil
}

// LoopbackHost reports whether a host or host:port names this machine only: "localhost" or a
// loopback IP. An empty host (":8080") listens on every interface, so it does not.
func LoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Unmap().IsLoopback()
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
	cfg := Config{HTTPAddr: ":8080", AutoMigrate: true, LogFormat: "text", AuthHeader: "Authorization", AuthServiceHeader: "Authorization", AuthGroupsClaim: "groups"}

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
	if cfg.DebugAllowRemote, err = boolEnv(lookup, "DEBUG_ALLOW_REMOTE", cfg.DebugAllowRemote); err != nil {
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
		"AUTH_HEADER": &cfg.AuthHeader, "AUTH_SERVICE_HEADER": &cfg.AuthServiceHeader, "AUTH_TRUSTED_PROXIES": &cfg.AuthTrustedProxies,
		"AUTH_ISSUER": &cfg.AuthIssuer, "AUTH_AUDIENCE": &cfg.AuthAudience,
		"AUTH_JWKS_URL": &cfg.AuthJWKSURL, "AUTH_GROUPS_CLAIM": &cfg.AuthGroupsClaim,
		"VIDISPINE_URL": &cfg.VidispineURL, "VIDISPINE_USERNAME": &cfg.VidispineUsername, "VIDISPINE_PASSWORD": &cfg.VidispinePassword,
	} {
		if v, ok := lookup(key); ok && v != "" {
			*dst = v
		}
	}
	if cfg.VidispineURL != "" && (cfg.VidispineUsername == "" || cfg.VidispinePassword == "") {
		return Config{}, ErrVidispineIncomplete
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
