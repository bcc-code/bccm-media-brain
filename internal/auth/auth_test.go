package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/auth/authtest"
	"github.com/bcc-media/mediabrain/internal/domain"
)

func verifier(i *authtest.Issuer) *JWKSVerifier {
	return &JWKSVerifier{Issuer: i.URL, Audience: authtest.Audience, JWKSURL: i.JWKSURL, GroupsClaim: "groups"}
}

func TestVerify(t *testing.T) {
	iss := authtest.NewIssuer()
	defer iss.Close()
	v := verifier(iss)
	ctx := context.Background()

	id, err := v.Verify(ctx, iss.Token("sub-1", map[string]any{"email": "a@example.test", "name": "A", "groups": []string{"g1", "g2"}}))
	if err != nil || id.Subject != "sub-1" || id.Email != "a@example.test" || id.Name != "A" || len(id.Groups) != 2 {
		t.Fatalf("valid token = %+v, %v", id, err)
	}
	if id, err := v.Verify(ctx, iss.Token("sub-2", map[string]any{"groups": "only"})); err != nil || len(id.Groups) != 1 {
		t.Fatalf("single group = %+v, %v", id, err)
	}

	now := time.Now()
	base := func() map[string]any {
		return map[string]any{"iss": iss.URL, "aud": authtest.Audience, "sub": "s", "exp": now.Add(time.Hour).Unix()}
	}
	with := func(k string, val any) map[string]any {
		c := base()
		if val == nil {
			delete(c, k)
		} else {
			c[k] = val
		}
		return c
	}
	other := authtest.NewIssuer()
	defer other.Close()
	none := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"s"}`)) + "."

	bad := map[string]string{
		"malformed":      "not-a-token",
		"alg none":       none,
		"expired":        iss.Sign(with("exp", now.Add(-time.Hour).Unix())),
		"no expiry":      iss.Sign(with("exp", nil)),
		"no subject":     iss.Sign(with("sub", nil)),
		"wrong issuer":   iss.Sign(with("iss", "https://evil.example")),
		"wrong audience": iss.Sign(with("aud", "someone-else")),
		"not yet valid":  iss.Sign(with("nbf", now.Add(time.Hour).Unix())),
		"foreign key":    other.Sign(base()),
		"tampered":       tamper(iss.Sign(base())),
	}
	for name, token := range bad {
		if _, err := v.Verify(ctx, token); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// tamper changes the payload without re-signing.
func tamper(token string) string {
	parts := strings.Split(token, ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"admin","exp":99999999999}`))
	return strings.Join(parts, ".")
}

func TestVerifyJWKSUnavailable(t *testing.T) {
	iss := authtest.NewIssuer()
	token := iss.Token("s", nil)
	down := httptest.NewServer(http.NotFoundHandler())
	defer down.Close()
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{")) }))
	defer garbage.Close()
	for name, url := range map[string]string{"404": down.URL, "bad json": garbage.URL, "unreachable": "http://127.0.0.1:1/jwks", "bad url": "://"} {
		v := verifier(iss)
		v.JWKSURL = url
		if _, err := v.Verify(context.Background(), token); err == nil || errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s: err = %v, want a fetch error", name, err)
		}
	}
	iss.Close()
}

type fakeUsers struct {
	signIns int
	err     error
}

func (f *fakeUsers) SignIn(_ context.Context, id access.Identity) (access.Principal, error) {
	f.signIns++
	return access.Principal{UserID: "US-" + id.Subject}, f.err
}

func (f *fakeUsers) Impersonate(_ context.Context, ref string) (access.Principal, error) {
	if ref == "ghost" {
		return access.Principal{}, domain.ErrNotFound
	}
	return access.Principal{UserID: ref}, f.err
}

func (f *fakeUsers) DebugAdmin(_ context.Context, email string) (access.Principal, error) {
	return access.Principal{UserID: "created:" + email, Admin: true}, f.err
}

type request struct {
	remote  string
	headers map[string]string
	cookie  string
}

func serve(opts Options, r request) (int, string) { return server(opts)(r) }

// server returns a function that sends one request through the same middleware instance.
func server(opts Options) func(request) (int, string) {
	var seen string
	h := Middleware(opts, http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		p, _ := access.From(req.Context())
		seen = p.UserID
	}))
	return func(r request) (int, string) {
		seen = ""
		return send(h, r), seen
	}
}

func send(h http.Handler, r request) int {
	req := httptest.NewRequest(http.MethodPost, "/query", nil)
	if r.remote != "" {
		req.RemoteAddr = r.remote
	}
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	if r.cookie != "" {
		req.AddCookie(&http.Cookie{Name: DebugUserCookie, Value: r.cookie})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestMiddlewareTokens(t *testing.T) {
	iss := authtest.NewIssuer()
	defer iss.Close()
	proxies, err := ParseProxies("10.0.0.0/8, 192.0.2.7 ,::1")
	if err != nil || len(proxies) != 3 {
		t.Fatalf("proxies = %v, %v", proxies, err)
	}
	users := &fakeUsers{}
	opts := Options{Header: "X-Auth-Token", TrustedProxies: proxies, Verifier: verifier(iss), Users: users}
	token := iss.Token("alice", nil)
	auth := map[string]string{"X-Auth-Token": "Bearer " + token}
	call := server(opts)

	for name, c := range map[string]struct {
		req    request
		status int
		user   string
	}{
		"trusted proxy":     {request{remote: "10.1.2.3:555", headers: auth}, 200, "US-alice"},
		"trusted single ip": {request{remote: "192.0.2.7:1", headers: map[string]string{"X-Auth-Token": token}}, 200, "US-alice"},
		"trusted ipv6":      {request{remote: "[::1]:1", headers: auth}, 200, "US-alice"},
		"untrusted peer":    {request{remote: "203.0.113.9:555", headers: auth}, 403, ""},
		"unparsable peer":   {request{remote: "nonsense", headers: auth}, 403, ""},
		"no token":          {request{remote: "10.1.2.3:555"}, 401, ""},
		"wrong header":      {request{remote: "10.1.2.3:555", headers: map[string]string{"Authorization": "Bearer " + token}}, 401, ""},
		"bad token":         {request{remote: "10.1.2.3:555", headers: map[string]string{"X-Auth-Token": "junk"}}, 401, ""},
		"debug header off":  {request{remote: "10.1.2.3:555", headers: map[string]string{DebugUserHeader: "US-1"}}, 401, ""},
		"debug cookie off":  {request{remote: "10.1.2.3:555", cookie: "US-1"}, 401, ""},
	} {
		if status, user := call(c.req); status != c.status || user != c.user {
			t.Errorf("%s: status %d user %q, want %d %q", name, status, user, c.status, c.user)
		}
	}
	if users.signIns != 1 {
		t.Errorf("the same token signed in %d times, want 1 (cached)", users.signIns)
	}

	users.err = domain.ErrForbidden
	if status, _ := serve(opts, request{remote: "10.1.2.3:1", headers: map[string]string{"X-Auth-Token": iss.Token("inactive", nil)}}); status != 403 {
		t.Errorf("inactive user: status %d", status)
	}
	users.err = errors.New("db down")
	if status, _ := serve(opts, request{remote: "10.1.2.3:1", headers: map[string]string{"X-Auth-Token": iss.Token("bob", nil)}}); status != 500 {
		t.Errorf("sign-in failure: status %d", status)
	}
}

func TestMiddlewareDebug(t *testing.T) {
	opts := Options{Header: "Authorization", Users: &fakeUsers{}, Debug: true}
	if status, user := serve(opts, request{headers: map[string]string{DebugUserHeader: "US-1"}}); status != 200 || user != "US-1" {
		t.Errorf("debug header: %d %q", status, user)
	}
	if status, user := serve(opts, request{cookie: "a@example.test"}); status != 200 || user != "a@example.test" {
		t.Errorf("debug cookie: %d %q", status, user)
	}
	if status, _ := serve(opts, request{cookie: "ghost"}); status != 401 {
		t.Errorf("unknown debug user: %d", status)
	}
	if status, _ := serve(opts, request{}); status != 401 {
		t.Errorf("nothing chosen: %d", status)
	}

	// With a default debug user, naming nobody (or naming that user) acts as them.
	opts.DebugUser = "dev@example.test"
	for name, r := range map[string]request{"nobody named": {}, "named": {headers: map[string]string{DebugUserHeader: "DEV@example.test"}}} {
		if status, user := serve(opts, r); status != 200 || user != "created:dev@example.test" {
			t.Errorf("default debug user, %s: %d %q", name, status, user)
		}
	}
	if status, user := serve(opts, request{cookie: "US-1"}); status != 200 || user != "US-1" {
		t.Errorf("an explicit choice wins over the default: %d %q", status, user)
	}
	opts.Debug = false
	if status, _ := serve(opts, request{}); status != 401 {
		t.Errorf("the default user must do nothing outside debug mode: %d", status)
	}
}

func TestParseProxiesRejects(t *testing.T) {
	if _, err := ParseProxies("10.0.0.0/8,not-an-ip"); err == nil {
		t.Fatal("expected error")
	}
	if got, err := ParseProxies(" , "); err != nil || len(got) != 0 {
		t.Fatalf("empty list = %v, %v", got, err)
	}
}
