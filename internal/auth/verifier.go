// Package auth turns the JWT forwarded by the auth proxy into an access.Principal.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	leeway        = 30 * time.Second
	minKeyRefetch = 10 * time.Second
	// How long a key set is trusted without asking again. This bounds how long a key that the
	// identity provider has withdrawn (for example after a compromise) is still accepted.
	jwksMaxAge = 5 * time.Minute
	// How long the last key set stays in use while refreshing fails. A shorter outage of the
	// identity provider does not sign everyone out; a longer one refuses every token, because
	// a withdrawn key must not stay trusted for as long as its endpoint can be kept unreachable.
	jwksMaxStale    = time.Hour
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

	now func() time.Time // nil: time.Now

	mu        sync.Mutex
	keys      jose.JSONWebKeySet
	fetched   time.Time     // when keys was requested
	attempted time.Time     // when the last fetch ended, successful or not
	fetchErr  error         // why the last fetch failed; nil when it succeeded
	inflight  chan struct{} // closed when the fetch in progress ends; nil when there is none
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
	expected := jwt.Expected{Issuer: v.Issuer, AnyAudience: jwt.Audience{v.Audience}, Time: v.clock()}
	if err := std.ValidateWithLeeway(expected, leeway); err != nil {
		return access.Identity{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	id := access.Identity{Subject: std.Subject, Expires: std.Expiry.Time().Add(leeway)}
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

// key returns the signing key with the given id from a key set that is at most jwksMaxAge old.
// It refetches when the set is older, or when the key is unknown (keys rotate), with at least
// minKeyRefetch between attempts. When a refresh fails, the cached set stays in use until it is
// jwksMaxStale old.
func (v *JWKSVerifier) key(ctx context.Context, kid string) (*jose.JSONWebKey, error) {
	for attempt := 0; ; attempt++ {
		v.mu.Lock()
		now := v.clock()
		age := now.Sub(v.fetched)
		have := !v.fetched.IsZero()
		keys := v.keys.Key(kid)
		if have && age < jwksMaxAge && len(keys) > 0 {
			v.mu.Unlock()
			return &keys[0], nil
		}
		inflight := v.inflight
		spaced := v.attempted.IsZero() || now.Sub(v.attempted) >= minKeyRefetch
		if inflight == nil && (attempt > 0 || !spaced) {
			usable, err := have && age < jwksMaxStale, v.fetchErr
			v.mu.Unlock()
			switch {
			case usable && len(keys) > 0:
				return &keys[0], nil
			case err != nil && !usable:
				return nil, err
			default:
				return nil, fmt.Errorf("%w: unknown key", ErrInvalidToken)
			}
		}
		if inflight == nil {
			inflight = make(chan struct{})
			v.inflight = inflight
			v.mu.Unlock()
			v.refresh(ctx, now, inflight)
			continue
		}
		v.mu.Unlock()
		select {
		case <-inflight:
		case <-ctx.Done():
			return nil, fmt.Errorf("auth: jwks: %w", ctx.Err())
		}
	}
}

// refresh fetches the key set without holding the mutex, so requests that can be answered
// from the cache are not held up by the identity provider.
func (v *JWKSVerifier) refresh(ctx context.Context, started time.Time, done chan struct{}) {
	// The result is shared with the callers waiting on done, so one of them giving up must
	// not fail it for the others.
	set, err := v.fetch(context.WithoutCancel(ctx))
	v.mu.Lock()
	defer v.mu.Unlock()
	v.attempted, v.fetchErr, v.inflight = v.clock(), err, nil
	if err == nil {
		// The start of the request: the answer cannot be newer than that.
		v.keys, v.fetched = set, started
	} else if !v.fetched.IsZero() {
		age := started.Sub(v.fetched)
		slog.WarnContext(ctx, "auth: jwks refresh failed", "error", err.Error(),
			"key_set_age", age.String(), "cached_keys_in_use", age < jwksMaxStale)
	}
	close(done)
}

func (v *JWKSVerifier) clock() time.Time {
	if v.now != nil {
		return v.now()
	}
	return time.Now()
}

func (v *JWKSVerifier) fetch(ctx context.Context) (jose.JSONWebKeySet, error) {
	var set jose.JSONWebKeySet
	ctx, cancel := context.WithTimeout(ctx, jwksHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.JWKSURL, nil)
	if err != nil {
		return set, fmt.Errorf("auth: jwks: %w", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return set, fmt.Errorf("auth: jwks: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return set, fmt.Errorf("auth: jwks: status %d", res.StatusCode)
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, res.Body, jwksBodyLimit)).Decode(&set); err != nil {
		return set, fmt.Errorf("auth: jwks: %w", err)
	}
	return set, nil
}
