package auth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/domain"
)

const (
	// DebugUserHeader and DebugUserCookie name the user (id or email) to act as in debug mode.
	DebugUserHeader = "X-Debug-User"
	DebugUserCookie = "mb_debug_user"

	principalTTL = time.Minute
)

type Verifier interface {
	Verify(ctx context.Context, token string) (access.Identity, error)
}

type Users interface {
	SignIn(ctx context.Context, id access.Identity) (access.Principal, error)
	Impersonate(ctx context.Context, ref string) (access.Principal, error)
	DebugAdmin(ctx context.Context, email string) (access.Principal, error)
}

type Options struct {
	Header         string
	TrustedProxies []netip.Prefix
	Verifier       Verifier // nil: only the debug bypass can authenticate
	Users          Users
	// Debug lets a request choose its user without a token. Development only.
	Debug bool
	// DebugUser is the email debug requests act as when they name no user. It is created as
	// an administrator on first use, so an empty database is usable straight away.
	DebugUser string
}

// ParseProxies reads a comma-separated list of IPs and CIDR ranges.
func ParseProxies(list string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if p, err := netip.ParsePrefix(item); err == nil {
			out = append(out, p)
			continue
		}
		addr, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("auth: trusted proxy %q is not an IP or CIDR", item)
		}
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

type cached struct {
	principal access.Principal
	until     time.Time
}

type middleware struct {
	Options
	next  http.Handler
	now   func() time.Time
	mu    sync.Mutex
	cache map[[sha256.Size]byte]cached
}

// Middleware authenticates every request and puts the principal into its context.
func Middleware(opts Options, next http.Handler) http.Handler {
	return &middleware{Options: opts, next: next, now: time.Now, cache: map[[sha256.Size]byte]cached{}}
}

func (m *middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, status, err := m.authenticate(r)
	if err != nil {
		slog.WarnContext(r.Context(), "auth: request refused", "status", status, "reason", err.Error(), "remote", r.RemoteAddr)
		code := "UNAUTHENTICATED"
		if status == http.StatusForbidden {
			code = "FORBIDDEN"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]any{{
			"message": strings.ToLower(http.StatusText(status)), "extensions": map[string]string{"code": code},
		}}})
		return
	}
	m.next.ServeHTTP(w, r.WithContext(access.With(r.Context(), p)))
}

func (m *middleware) authenticate(r *http.Request) (access.Principal, int, error) {
	if m.Debug {
		ref := r.Header.Get(DebugUserHeader)
		if c, err := r.Cookie(DebugUserCookie); ref == "" && err == nil {
			ref = c.Value
		}
		if m.DebugUser != "" && (ref == "" || strings.EqualFold(ref, m.DebugUser)) {
			p, err := m.Users.DebugAdmin(r.Context(), m.DebugUser)
			return p, statusOf(err), err
		}
		if ref != "" {
			p, err := m.Users.Impersonate(r.Context(), ref)
			return p, statusOf(err), err
		}
	}
	if m.Verifier == nil {
		return access.Principal{}, http.StatusUnauthorized, errors.New("no debug user chosen and no token verification configured")
	}
	if !m.trusted(r.RemoteAddr) {
		return access.Principal{}, http.StatusForbidden, errors.New("peer is not a trusted proxy")
	}
	token := strings.TrimSpace(r.Header.Get(m.Header))
	if len(token) > 7 && strings.EqualFold(token[:7], "bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	if token == "" {
		return access.Principal{}, http.StatusUnauthorized, errors.New("no token")
	}
	key := sha256.Sum256([]byte(token))
	now := m.now()
	m.mu.Lock()
	hit, ok := m.cache[key]
	m.mu.Unlock()
	if ok && now.Before(hit.until) {
		return hit.principal, 0, nil
	}
	id, err := m.Verifier.Verify(r.Context(), token)
	if err != nil {
		return access.Principal{}, http.StatusUnauthorized, err
	}
	p, err := m.Users.SignIn(r.Context(), id)
	if err != nil {
		return access.Principal{}, statusOf(err), err
	}
	m.mu.Lock()
	for k, c := range m.cache {
		if now.After(c.until) {
			delete(m.cache, k)
		}
	}
	// A cache hit skips verification, so the entry must not outlive the token: without the
	// bound, a token verified just before it expires would work for another principalTTL.
	until := now.Add(principalTTL)
	if id.Expires.Before(until) {
		until = id.Expires
	}
	if now.Before(until) {
		m.cache[key] = cached{principal: p, until: until}
	}
	m.mu.Unlock()
	return p, 0, nil
}

func statusOf(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrUnauthenticated):
		return http.StatusUnauthorized
	}
	return http.StatusInternalServerError
}

func (m *middleware) trusted(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, p := range m.TrustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
