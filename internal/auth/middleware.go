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
	// ServiceSignIn also returns when the token expires.
	ServiceSignIn(ctx context.Context, token string) (access.Principal, time.Time, error)
	Impersonate(ctx context.Context, ref string) (access.Principal, error)
	DebugAdmin(ctx context.Context, email string) (access.Principal, error)
}

type Options struct {
	Header string
	// ServiceHeader is where machine clients send their service token. Empty: only Header.
	ServiceHeader string
	// ClientHeader is the header in which the proxy reports the address of its client. It is
	// read only from a trusted proxy and only recorded. Empty: the peer's address is recorded.
	ClientHeader   string
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

// maxClientLen bounds what a proxy's client header may add to every log line of a request.
const maxClientLen = 128

// client is where the request came from, for the logs: what a trusted proxy reports in
// ClientHeader, otherwise the peer. A header from anyone else would let a caller choose what
// the audit log says about them.
func (m *middleware) client(r *http.Request) string {
	if m.ClientHeader != "" && m.trusted(r.RemoteAddr) {
		if v := strings.TrimSpace(r.Header.Get(m.ClientHeader)); v != "" {
			if len(v) > maxClientLen {
				v = v[:maxClientLen]
			}
			return v
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (m *middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := access.OriginOf(r.Context())
	if origin != nil {
		origin.Client = m.client(r)
	}
	p, status, err := m.authenticate(r)
	if err != nil {
		slog.WarnContext(r.Context(), "auth: request refused", append([]any{"status", status, "reason", err.Error(), "remote", r.RemoteAddr},
			access.OriginAttrs(r.Context())...)...)
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
	if origin != nil {
		origin.User, origin.Token = p.UserID, p.TokenID
	}
	m.next.ServeHTTP(w, r.WithContext(access.With(r.Context(), p)))
}

func (m *middleware) authenticate(r *http.Request) (access.Principal, int, error) {
	// Before the debug bypass, so a token can be tried in development with DEBUG_USER set.
	if token := m.serviceToken(r); token != "" {
		if !m.Debug && !m.trusted(r.RemoteAddr) {
			return access.Principal{}, http.StatusForbidden, errors.New("peer is not a trusted proxy")
		}
		return m.cached(token, func() (access.Principal, time.Time, int, error) {
			p, expires, err := m.Users.ServiceSignIn(r.Context(), token)
			return p, expires, statusOf(err), err
		})
	}
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
	token := bearer(r.Header.Get(m.Header))
	if token == "" {
		return access.Principal{}, http.StatusUnauthorized, errors.New("no token")
	}
	return m.cached(token, func() (access.Principal, time.Time, int, error) {
		id, err := m.Verifier.Verify(r.Context(), token)
		if err != nil {
			return access.Principal{}, time.Time{}, http.StatusUnauthorized, err
		}
		p, err := m.Users.SignIn(r.Context(), id)
		return p, id.Expires, statusOf(err), err
	})
}

func bearer(header string) string {
	token := strings.TrimSpace(header)
	if len(token) > 7 && strings.EqualFold(token[:7], "bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	return token
}

// serviceToken returns the service token a machine client sent, or "". A proxy that puts its
// own JWT in another header leaves ServiceHeader to the client, so both are looked at.
func (m *middleware) serviceToken(r *http.Request) string {
	for _, h := range []string{m.ServiceHeader, m.Header} {
		if h == "" {
			continue
		}
		if token := bearer(r.Header.Get(h)); access.IsServiceToken(token) {
			return token
		}
	}
	return ""
}

// cached remembers the principal of a token for principalTTL, so a busy client costs one
// sign-in per minute, and a change to the user or a revoked token takes that long to apply.
// signIn also says when the token expires.
func (m *middleware) cached(token string, signIn func() (access.Principal, time.Time, int, error)) (access.Principal, int, error) {
	key := sha256.Sum256([]byte(token))
	now := m.now()
	m.mu.Lock()
	hit, ok := m.cache[key]
	m.mu.Unlock()
	if ok && now.Before(hit.until) {
		return hit.principal, 0, nil
	}
	p, expires, status, err := signIn()
	if err != nil {
		return access.Principal{}, status, err
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
	if expires.Before(until) {
		until = expires
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
