package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/auth/authtest"
)

// cachingServer is a middleware and its verifier on one hand-moved clock, with a function
// that sends a token and one that signs a token living for the given time.
func cachingServer(t *testing.T) (*clock, *fakeUsers, func(token string) int, func(sub string, life time.Duration) string) {
	t.Helper()
	iss := authtest.NewIssuer()
	t.Cleanup(iss.Close)
	c := &clock{t: time.Now()}
	v := verifier(iss)
	v.now = c.now
	users := &fakeUsers{}
	proxies, _ := ParseProxies("10.0.0.0/8")
	h := Middleware(Options{Header: "Authorization", TrustedProxies: proxies, Verifier: v, Users: users},
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.(*middleware).now = c.now
	call := func(token string) int {
		return send(h, request{remote: "10.0.0.1:1", headers: map[string]string{"Authorization": token}})
	}
	sign := func(sub string, life time.Duration) string {
		return iss.Sign(map[string]any{"iss": iss.URL, "aud": authtest.Audience, "sub": sub, "exp": c.now().Add(life).Unix()})
	}
	return c, users, call, sign
}

func TestCachedUserDoesNotOutliveTheToken(t *testing.T) {
	c, users, call, sign := cachingServer(t)
	// Accepted until exp + leeway, which is before the cache entry would otherwise end.
	token := sign("alice", 10*time.Second)
	if status := call(token); status != 200 {
		t.Fatalf("fresh token: status %d", status)
	}
	c.advance(10*time.Second + leeway - time.Second)
	if status := call(token); status != 200 || users.signIns != 1 {
		t.Fatalf("inside the leeway: status %d, %d sign-ins, want 200 from the cache", status, users.signIns)
	}
	c.advance(2 * time.Second)
	if status := call(token); status != 401 {
		t.Fatalf("expired token inside the cache lifetime: status %d, want 401", status)
	}
}

func TestCachedUserIsLookedUpAgainAfterTheTTL(t *testing.T) {
	c, users, call, sign := cachingServer(t)
	token := sign("alice", time.Hour)
	for _, step := range []struct {
		advance time.Duration
		signIns int
	}{{0, 1}, {principalTTL - time.Second, 1}, {2 * time.Second, 2}} {
		c.advance(step.advance)
		if status := call(token); status != 200 || users.signIns != step.signIns {
			t.Fatalf("after %v: status %d, %d sign-ins, want 200 and %d", step.advance, status, users.signIns, step.signIns)
		}
	}
}

func TestCachedServiceAccountDoesNotOutliveTheToken(t *testing.T) {
	c, users, call, _ := cachingServer(t)
	users.serviceExpires = c.now().Add(10 * time.Second)
	for _, step := range []struct {
		advance time.Duration
		signIns int
	}{{0, 1}, {9 * time.Second, 1}, {2 * time.Second, 2}} {
		c.advance(step.advance)
		if status := call("Bearer mbs_abc"); status != 200 || users.serviceSignIns != step.signIns {
			t.Fatalf("after %v: status %d, %d sign-ins, want %d (the token is checked again once it expired)",
				step.advance, status, users.serviceSignIns, step.signIns)
		}
	}
}

type noExpiry struct{}

func (noExpiry) Verify(context.Context, string) (access.Identity, error) {
	return access.Identity{Subject: "alice"}, nil
}

func TestIdentityWithoutExpiryIsNotCached(t *testing.T) {
	users := &fakeUsers{}
	proxies, _ := ParseProxies("10.0.0.0/8")
	call := server(Options{Header: "Authorization", TrustedProxies: proxies, Verifier: noExpiry{}, Users: users})
	for range 2 {
		if status, _ := call(request{remote: "10.0.0.1:1", headers: map[string]string{"Authorization": "t"}}); status != 200 {
			t.Fatalf("status %d", status)
		}
	}
	if users.signIns != 2 {
		t.Fatalf("%d sign-ins, want 2: nothing says how long the identity holds", users.signIns)
	}
}
