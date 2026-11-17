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
	return &JWKSVerifier{Issuer: i.URL, Audience: authtest.Audience, JWKSURL: i.JWKSURL, GroupsClaim: "groups",
		UserGroup: authtest.UserGroup, ServiceGroup: authtest.ServiceGroup}
}

func TestVerify(t *testing.T) {
	iss := authtest.NewIssuer()
	defer iss.Close()
	v := verifier(iss)
	ctx := context.Background()

	id, err := v.Verify(ctx, iss.Token("sub-1", map[string]any{
		"email": "a@example.test", "name": "A", "preferred_username": "a", "groups": []string{"g1", authtest.UserGroup}}))
	if err != nil || id.Subject != "sub-1" || id.Email != "a@example.test" || id.Name != "A" || id.Username != "a" || len(id.Groups) != 2 || id.Service {
		t.Fatalf("valid token = %+v, %v", id, err)
	}
	if id, err := v.Verify(ctx, iss.Token("sub-2", map[string]any{"groups": authtest.UserGroup})); err != nil || len(id.Groups) != 1 {
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

// The two groups decide who is let in and as what. A machine must not get in as a person, who
// needs no account made in advance, so the service group wins.
func TestVerifyGroups(t *testing.T) {
	iss := authtest.NewIssuer()
	defer iss.Close()
	ctx := context.Background()
	token := func(groups ...string) string { return iss.Token("s", map[string]any{"groups": groups}) }

	v := verifier(iss)
	for name, c := range map[string]struct {
		token   string
		service bool
		err     error
	}{
		"person":             {token("x", authtest.UserGroup), false, nil},
		"service":            {token(authtest.ServiceGroup), true, nil},
		"both":               {token(authtest.UserGroup, authtest.ServiceGroup), true, nil},
		"another group":      {token("x"), false, ErrNotAllowed},
		"no groups":          {token(), false, ErrNotAllowed},
		"no claim":           {iss.Token("s", map[string]any{"groups": nil}), false, ErrNotAllowed},
		"not a list of text": {iss.Token("s", map[string]any{"groups": []int{1}}), false, ErrNotAllowed},
	} {
		id, err := v.Verify(ctx, c.token)
		if !errors.Is(err, c.err) || id.Service != c.service {
			t.Errorf("%s: service %v, err %v; want %v, %v", name, id.Service, err, c.service, c.err)
		}
	}

	// A group that is not configured matches nobody, not the token that names an empty group.
	v.ServiceGroup = ""
	if id, err := v.Verify(ctx, token("", authtest.UserGroup)); err != nil || id.Service {
		t.Errorf("no service group configured: %+v, %v", id, err)
	}
	if _, err := v.Verify(ctx, token(authtest.ServiceGroup)); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("a service token without a service group configured: %v", err)
	}
	v.UserGroup = ""
	if _, err := v.Verify(ctx, token("")); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("no user group configured: %v", err)
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
	if id.Service {
		return access.Principal{UserID: "US-svc:" + id.Subject, Service: true}, f.err
	}
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
		// What the proxy also sends next to its JWT. Unsigned, so it must never sign anyone in.
		"identity headers": {request{remote: "10.1.2.3:555", headers: map[string]string{
			"X-authentik-uid": "alice", "X-authentik-email": "alice@example.test", "X-authentik-groups": "it-admins"}}, 401, ""},
		"debug header off": {request{remote: "10.1.2.3:555", headers: map[string]string{DebugUserHeader: "US-1"}}, 401, ""},
		"debug cookie off": {request{remote: "10.1.2.3:555", cookie: "US-1"}, 401, ""},
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

// A machine client signs in with the same kind of token as a person, in the same header.
func TestMiddlewareServiceAccounts(t *testing.T) {
	iss := authtest.NewIssuer()
	defer iss.Close()
	proxies, _ := ParseProxies("10.0.0.0/8")
	users := &fakeUsers{}
	opts := Options{Header: "X-Auth-Token", TrustedProxies: proxies, Verifier: verifier(iss), Users: users}
	call := server(opts)
	const proxy = "10.1.2.3:555"
	service := iss.Token("flows", map[string]any{"groups": []string{authtest.ServiceGroup}})
	stranger := iss.Token("mallory", map[string]any{"groups": []string{"somewhere-else"}})

	for _, c := range []struct {
		name   string
		req    request
		status int
		user   string
	}{
		{"service group", request{remote: proxy, headers: map[string]string{"X-Auth-Token": service}}, 200, "US-svc:flows"},
		{"neither group", request{remote: proxy, headers: map[string]string{"X-Auth-Token": stranger}}, 403, ""},
		{"refused again", request{remote: proxy, headers: map[string]string{"X-Auth-Token": stranger}}, 403, ""},
		{"untrusted peer", request{remote: "203.0.113.9:555", headers: map[string]string{"X-Auth-Token": service}}, 403, ""},
		// MediaBrain issued tokens of its own once. Nothing reads them or their header now.
		{"a token of the old kind", request{remote: proxy, headers: map[string]string{"Authorization": "Bearer mbs_abc"}}, 401, ""},
		{"in the token header", request{remote: proxy, headers: map[string]string{"X-Auth-Token": "mbs_abc"}}, 401, ""},
	} {
		if status, user := call(c.req); status != c.status || user != c.user {
			t.Errorf("%s: status %d user %q, want %d %q", c.name, status, user, c.status, c.user)
		}
	}
	if users.signIns != 1 {
		t.Errorf("sign-ins = %d, want 1: a refused token never reaches sign-in", users.signIns)
	}

	// The account does not exist, or is switched off: sign-in says so.
	users.err = domain.ErrForbidden
	other := iss.Token("unknown", map[string]any{"groups": []string{authtest.ServiceGroup}})
	if status, _ := call(request{remote: proxy, headers: map[string]string{"X-Auth-Token": other}}); status != 403 {
		t.Errorf("a service account that was not created: status %d", status)
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

// The audit log names the client. Only a trusted proxy may say who that is: anyone else would
// be choosing what the log says about them.
func TestMiddlewareRecordsTheClient(t *testing.T) {
	proxies, _ := ParseProxies("10.0.0.0/8")
	iss := authtest.NewIssuer()
	defer iss.Close()
	token := iss.Token("alice", nil)
	const user = "US-alice"
	long := strings.Repeat("9", 300)
	for name, c := range map[string]struct {
		header, remote, sent string
		debug                bool
		want                 string
		user                 string
	}{
		"the proxy's word":        {"X-Real-IP", "10.1.2.3:555", "203.0.113.9", false, "203.0.113.9", user},
		"no header configured":    {"", "10.1.2.3:555", "203.0.113.9", false, "10.1.2.3", user},
		"the proxy sent nothing":  {"X-Real-IP", "10.1.2.3:555", "", false, "10.1.2.3", user},
		"a header of any length":  {"X-Real-IP", "10.1.2.3:555", long, false, long[:128], user},
		"a peer that is no proxy": {"X-Real-IP", "192.0.2.7:555", "203.0.113.9", true, "192.0.2.7", "US-debug"},
		"a refused request":       {"X-Real-IP", "192.0.2.7:555", "203.0.113.9", false, "192.0.2.7", ""},
		"a peer without a port":   {"", "10.1.2.3", "", false, "10.1.2.3", user},
	} {
		origin := &access.Origin{RequestID: "r1"}
		h := Middleware(Options{Header: "Authorization", ClientHeader: c.header, TrustedProxies: proxies, Verifier: verifier(iss), Users: &fakeUsers{}, Debug: c.debug},
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		req := httptest.NewRequest(http.MethodPost, "/query", nil)
		req.RemoteAddr = c.remote
		req.Header.Set("Authorization", token)
		req.Header.Set(DebugUserHeader, "US-debug")
		if c.sent != "" {
			req.Header.Set("X-Real-IP", c.sent)
		}
		h.ServeHTTP(httptest.NewRecorder(), req.WithContext(access.WithOrigin(req.Context(), origin)))
		if origin.Client != c.want || origin.User != c.user || origin.RequestID != "r1" {
			t.Errorf("%s: client %q, user %q; want %q, %q", name, origin.Client, origin.User, c.want, c.user)
		}
	}
}
