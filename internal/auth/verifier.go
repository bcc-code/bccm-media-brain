// Package auth turns the JWT forwarded by the auth proxy into an access.Principal.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/bcc-media/mediabrain/internal/access"
)

// ErrInvalidToken covers every reason a token is not accepted.
var ErrInvalidToken = errors.New("auth: invalid token")

const (
	leeway          = 30 * time.Second
	minKeyRefetch   = 10 * time.Second
	jwksBodyLimit   = 1 << 20
	jwksHTTPTimeout = 10 * time.Second
)

// Only asymmetric algorithms: a shared secret would let anyone holding it mint tokens.
var algorithms = []jose.SignatureAlgorithm{
	jose.RS256, jose.RS384, jose.RS512, jose.PS256, jose.PS384, jose.PS512,
	jose.ES256, jose.ES384, jose.ES512, jose.EdDSA,
}

// JWKSVerifier checks tokens against the keys published at a JWKS URL.
type JWKSVerifier struct {
	Issuer      string
	Audience    string
	JWKSURL     string
	GroupsClaim string

	mu      sync.Mutex
	keys    jose.JSONWebKeySet
	fetched time.Time
}

func (v *JWKSVerifier) Verify(ctx context.Context, raw string) (access.Identity, error) {
	tok, err := jwt.ParseSigned(raw, algorithms)
	if err != nil || len(tok.Headers) == 0 {
		return access.Identity{}, fmt.Errorf("%w: malformed", ErrInvalidToken)
	}
	key, err := v.key(ctx, tok.Headers[0].KeyID)
	if err != nil {
		return access.Identity{}, err
	}
	var std jwt.Claims
	extra := map[string]any{}
	if err := tok.Claims(key, &std, &extra); err != nil {
		return access.Identity{}, fmt.Errorf("%w: bad signature", ErrInvalidToken)
	}
	if std.Expiry == nil || std.Subject == "" {
		return access.Identity{}, fmt.Errorf("%w: exp and sub are required", ErrInvalidToken)
	}
	expected := jwt.Expected{Issuer: v.Issuer, AnyAudience: jwt.Audience{v.Audience}, Time: time.Now()}
	if err := std.ValidateWithLeeway(expected, leeway); err != nil {
		return access.Identity{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	id := access.Identity{Subject: std.Subject}
	id.Email, _ = extra["email"].(string)
	id.Name, _ = extra["name"].(string)
	switch groups := extra[v.GroupsClaim].(type) {
	case string:
		id.Groups = []string{groups}
	case []any:
		for _, g := range groups {
			if s, ok := g.(string); ok {
				id.Groups = append(id.Groups, s)
			}
		}
	}
	return id, nil
}

// key returns the signing key with the given id, refetching the key set when it is unknown
// (keys rotate), but not more often than minKeyRefetch.
func (v *JWKSVerifier) key(ctx context.Context, kid string) (*jose.JSONWebKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for attempt := 0; ; attempt++ {
		if keys := v.keys.Key(kid); len(keys) > 0 {
			return &keys[0], nil
		}
		if attempt > 0 || time.Since(v.fetched) < minKeyRefetch {
			return nil, fmt.Errorf("%w: unknown key", ErrInvalidToken)
		}
		if err := v.fetch(ctx); err != nil {
			return nil, err
		}
	}
}

func (v *JWKSVerifier) fetch(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, jwksHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.JWKSURL, nil)
	if err != nil {
		return fmt.Errorf("auth: jwks: %w", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("auth: jwks: %w", err)
	}
	defer res.Body.Close()
	var set jose.JSONWebKeySet
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("auth: jwks: status %d", res.StatusCode)
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, res.Body, jwksBodyLimit)).Decode(&set); err != nil {
		return fmt.Errorf("auth: jwks: %w", err)
	}
	v.keys, v.fetched = set, time.Now()
	return nil
}
