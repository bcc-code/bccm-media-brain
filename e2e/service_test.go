//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/auth"
	"github.com/bcc-media/mediabrain/internal/cli"
	"github.com/bcc-media/mediabrain/internal/config"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/devdata"
)

const (
	createServiceAccount = `mutation($in: ServiceAccountInput!) { createServiceAccount(input: $in) { id kind externalId name email isAdmin active } }`
	createServiceToken   = `mutation($in: ServiceTokenInput!) { createServiceToken(input: $in) { token serviceToken { id userId name createdBy expiresAt lastUsedAt revokedAt } } }`
	revokeServiceToken   = `mutation($id: ID!) { revokeServiceToken(id: $id) { id revokedAt } }`
	setServiceActive     = `mutation($s: String!, $a: Boolean!) { setServiceAccountActive(serviceAccount: $s, active: $a) { id active } }`
	listServiceTokens    = `query($s: String!) { serviceTokens(serviceAccount: $s) { id name revokedAt lastUsedAt } }`
)

type serviceTokenJSON struct {
	Token        string
	ServiceToken struct {
		ID, UserID, Name      string
		CreatedBy             *string
		ExpiresAt             time.Time
		LastUsedAt, RevokedAt *time.Time
	}
}

func newServiceToken(t *testing.T, admin user, account string) serviceTokenJSON {
	t.Helper()
	var out struct{ CreateServiceToken serviceTokenJSON }
	admin.must(t, createServiceToken, map[string]any{"in": map[string]any{"serviceAccount": account, "name": "e2e"}}, &out)
	return out.CreateServiceToken
}

// signInService creates a service account with a token. sub is the account's name.
func signInService(t *testing.T, admin bool) user {
	t.Helper()
	a := signInAdmin(t)
	name := "svc-" + unique()
	var out struct{ CreateServiceAccount struct{ ID string } }
	a.must(t, createServiceAccount, map[string]any{"in": map[string]any{"name": name, "admin": admin}}, &out)
	return user{id: out.CreateServiceAccount.ID, sub: name, token: newServiceToken(t, a, name).Token}
}

func TestServiceAccountsAndTokens(t *testing.T) {
	ctx := context.Background()
	admin := signInAdmin(t)
	name := "ingest-" + unique()

	var acc struct {
		CreateServiceAccount struct {
			ID, Kind, ExternalID, Name string
			Email                      *string
			IsAdmin, Active            bool
		}
	}
	admin.must(t, createServiceAccount, map[string]any{"in": map[string]any{"name": " " + name + " "}}, &acc)
	a := acc.CreateServiceAccount
	if a.Kind != "SERVICE" || a.ExternalID != "service|"+name || a.Name != name || a.Email != nil || a.IsAdmin || !a.Active {
		t.Fatalf("account = %+v", a)
	}
	admin.fails(t, "CONFLICT", createServiceAccount, map[string]any{"in": map[string]any{"name": name}})
	admin.fails(t, "INVALID_INPUT", createServiceAccount, map[string]any{"in": map[string]any{"name": "  "}})
	admin.fails(t, "INVALID_INPUT", createServiceAccount, map[string]any{"in": map[string]any{"name": "US-looks-like-an-id"}})

	tokenVars := func(account string, more map[string]any) map[string]any {
		in := map[string]any{"serviceAccount": account, "name": "prod"}
		for k, v := range more {
			in[k] = v
		}
		return map[string]any{"in": in}
	}
	admin.fails(t, "NOT_FOUND", createServiceToken, tokenVars("no-such-account", nil))
	admin.fails(t, "NOT_FOUND", createServiceToken, tokenVars(admin.id, nil)) // a person is not a service account
	admin.fails(t, "INVALID_INPUT", createServiceToken, tokenVars(name, map[string]any{"name": ""}))
	admin.fails(t, "INVALID_INPUT", createServiceToken, tokenVars(name, map[string]any{"expiresAt": time.Now().Add(-time.Minute)}))

	var created struct{ CreateServiceToken serviceTokenJSON }
	admin.must(t, createServiceToken, tokenVars(a.ID, nil), &created)
	tok := created.CreateServiceToken
	st := tok.ServiceToken
	year := time.Until(st.ExpiresAt)
	if !access.IsServiceToken(tok.Token) || st.UserID != a.ID || st.Name != "prod" || st.CreatedBy == nil || *st.CreatedBy != admin.id ||
		st.LastUsedAt != nil || st.RevokedAt != nil || year < 364*24*time.Hour || year > 366*24*time.Hour {
		t.Fatalf("token = %+v", tok)
	}
	soon := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	admin.must(t, createServiceToken, tokenVars(name, map[string]any{"expiresAt": soon}), &created)
	if !created.CreateServiceToken.ServiceToken.ExpiresAt.Equal(soon) {
		t.Fatalf("expiresAt = %v, want %v", created.CreateServiceToken.ServiceToken.ExpiresAt, soon)
	}

	// The database holds the hash, never the token.
	var hashed, plain bool
	err := pool.QueryRow(ctx, `SELECT token_hash = sha256(convert_to($2, 'UTF8')), position(convert_to($2, 'UTF8') in token_hash) > 0
		FROM service_token WHERE id = $1`, st.ID, tok.Token).Scan(&hashed, &plain)
	if err != nil || !hashed || plain {
		t.Fatalf("stored hash: hashed %v, plain %v, %v", hashed, plain, err)
	}

	// The token is the account.
	svc := user{id: a.ID, token: tok.Token}
	var me struct{ Me struct{ ID, Kind string } }
	svc.must(t, `{ me { id kind } }`, nil, &me)
	if me.Me.ID != a.ID || me.Me.Kind != "SERVICE" {
		t.Fatalf("me = %+v", me.Me)
	}
	var list struct {
		ServiceTokens []struct {
			ID         string
			LastUsedAt *time.Time
		}
		ServiceAccounts []struct{ ID, Kind string }
	}
	admin.must(t, `query($s: String!) { serviceTokens(serviceAccount: $s) { id lastUsedAt } serviceAccounts { id kind } }`, map[string]any{"s": name}, &list)
	if len(list.ServiceTokens) != 2 || list.ServiceTokens[0].ID != st.ID || list.ServiceTokens[0].LastUsedAt == nil || list.ServiceTokens[1].LastUsedAt != nil {
		t.Fatalf("tokens = %+v", list.ServiceTokens)
	}
	found := false
	for _, s := range list.ServiceAccounts {
		found = found || s.ID == a.ID
		if s.Kind != "SERVICE" {
			t.Fatalf("serviceAccounts lists %+v", s)
		}
	}
	if !found {
		t.Fatal("the account is missing from serviceAccounts")
	}
	admin.fails(t, "NOT_FOUND", listServiceTokens, map[string]any{"s": admin.id})

	// Revoking is final and keeps the first time.
	var rev struct {
		RevokeServiceToken struct{ RevokedAt *time.Time }
	}
	fresh := newServiceToken(t, admin, name)
	admin.must(t, revokeServiceToken, map[string]any{"id": fresh.ServiceToken.ID}, &rev)
	first := rev.RevokeServiceToken.RevokedAt
	admin.must(t, revokeServiceToken, map[string]any{"id": fresh.ServiceToken.ID}, &rev)
	if first == nil || !first.Equal(*rev.RevokeServiceToken.RevokedAt) {
		t.Fatalf("revokedAt = %v then %v", first, rev.RevokeServiceToken.RevokedAt)
	}
	admin.fails(t, "NOT_FOUND", revokeServiceToken, map[string]any{"id": "TK-00000000000000000000000000"})
}

// Every reason to refuse a service token looks the same from outside. The tokens here are
// used for the first time after they stopped being valid: an accepted token stays accepted
// for up to a minute (the principal cache).
func TestRefusedServiceTokens(t *testing.T) {
	ctx := context.Background()
	admin := signInAdmin(t)
	svc := signInService(t, false)

	revoked := newServiceToken(t, admin, svc.sub)
	admin.must(t, revokeServiceToken, map[string]any{"id": revoked.ServiceToken.ID}, nil)
	expired := newServiceToken(t, admin, svc.sub)
	if _, err := pool.Exec(ctx, `UPDATE service_token SET expires_at = now() - interval '1 second' WHERE id = $1`, expired.ServiceToken.ID); err != nil {
		t.Fatal(err)
	}
	off := signInService(t, true)
	offToken := newServiceToken(t, admin, off.sub)
	var set struct{ SetServiceAccountActive struct{ Active bool } }
	admin.must(t, setServiceActive, map[string]any{"s": off.sub, "a": false}, &set)
	if set.SetServiceAccountActive.Active {
		t.Fatal("still active")
	}

	bodies := map[string]bool{}
	for name, token := range map[string]string{
		"unknown":          access.TokenPrefix + strings.Repeat("A", 43),
		"revoked":          revoked.Token,
		"expired":          expired.Token,
		"inactive account": offToken.Token,
		"prefix only":      access.TokenPrefix,
	} {
		status, r := post(t, map[string]string{"Authorization": "Bearer " + token}, `{ ping }`, nil)
		if status != http.StatusUnauthorized || len(r.Errors) != 1 || r.Errors[0].Extensions.Code != "UNAUTHENTICATED" || len(r.Data) != 0 {
			t.Errorf("%s: status %d, body %+v", name, status, r)
		}
		body, _ := json.Marshal(r)
		bodies[string(body)] = true
	}
	if len(bodies) != 1 {
		t.Errorf("refusals differ: %v", bodies)
	}

	// Switching the account back on makes its token work again.
	admin.must(t, setServiceActive, map[string]any{"s": off.id, "a": true}, nil)
	user{token: offToken.Token}.must(t, `{ me { id } }`, nil, nil)
	admin.fails(t, "NOT_FOUND", setServiceActive, map[string]any{"s": admin.id, "a": false})
	admin.fails(t, "NOT_FOUND", setServiceActive, map[string]any{"s": "no-such-account", "a": false})
}

func TestServiceAccountsUseGrants(t *testing.T) {
	w := newWorld(t, 61)
	svc := signInService(t, false)

	svc.isNull(t, "asset", w.c1.asset)
	svc.fails(t, "FORBIDDEN", `{ files { id } }`, nil)
	if got := w.visible(t, svc); got != "" {
		t.Fatalf("without a grant the account sees %q", got)
	}
	grant(t, w.admin, "PROJECT", w.projectA, "VIEW", to(svc))
	svc.sees(t, "asset", w.c1.asset)
	if got := w.visible(t, svc); got != "c1,c2" {
		t.Fatalf("with view on project A the account sees %q", got)
	}
	svc.fails(t, "FORBIDDEN", updateAsset, map[string]any{"id": w.c1.asset, "in": map[string]any{"typeId": 1, "title": "x"}})
	grant(t, w.admin, "ASSET", w.c1.asset, "EDIT", to(svc))
	svc.must(t, updateAsset, map[string]any{"id": w.c1.asset, "in": map[string]any{"typeId": 1, "title": w.marker + " by a machine"}}, nil)
}

// An administrator service account does what administrators do, except what would let its
// token outlive a revocation: credentials and administrators.
func TestServiceAccountsCannotManageCredentials(t *testing.T) {
	w := newWorld(t, 62)
	svc := signInService(t, true)
	other := signInService(t, false)
	person := signIn(t)

	svc.must(t, `{ users { id } files { id } }`, nil, nil)
	svc.must(t, registerFile, map[string]any{"in": fileVars(w.f.VideoFile())}, nil)

	token := newServiceToken(t, w.admin, other.sub)
	denied := map[string]struct {
		query string
		vars  map[string]any
	}{
		"create account": {createServiceAccount, map[string]any{"in": map[string]any{"name": "x" + unique()}}},
		"create token":   {createServiceToken, map[string]any{"in": map[string]any{"serviceAccount": svc.id, "name": "more"}}},
		"revoke token":   {revokeServiceToken, map[string]any{"id": token.ServiceToken.ID}},
		"deactivate":     {setServiceActive, map[string]any{"s": other.id, "a": false}},
		"list tokens":    {listServiceTokens, map[string]any{"s": other.id}},
		"list accounts":  {`{ serviceAccounts { id } }`, nil},
		"make admin":     {`mutation($u: String!) { setUserAdmin(user: $u, admin: true) { id } }`, map[string]any{"u": person.id}},
	}
	for name, c := range denied {
		t.Run(name, func(t *testing.T) {
			svc.fails(t, "FORBIDDEN", c.query, c.vars)
			person.fails(t, "FORBIDDEN", c.query, c.vars)
		})
	}

	// Acting as the account through the debug bypass is still acting as a machine.
	asService := map[string]string{auth.DebugUserHeader: svc.id}
	_, r := post(t, asService, `{ me { kind } }`, nil)
	if !strings.Contains(string(r.Data), "SERVICE") {
		t.Fatalf("debug bypass as a service account: %s %+v", r.Data, r.Errors)
	}
	if _, r := post(t, asService, `{ serviceAccounts { id } }`, nil); len(r.Errors) != 1 || r.Errors[0].Extensions.Code != "FORBIDDEN" {
		t.Fatalf("debug bypass as a service account may manage credentials: %+v", r.Errors)
	}
}

// A token from the identity provider cannot become a service account by naming it.
func TestSignInCannotBecomeAServiceAccount(t *testing.T) {
	svc := signInService(t, true)
	headers := map[string]string{"Authorization": "Bearer " + tokenFor("service|"+svc.sub, "thief-"+unique()+"@example.test")}
	status, r := post(t, headers, `{ me { id } }`, nil)
	if status != http.StatusForbidden || len(r.Data) != 0 {
		t.Fatalf("status %d, body %+v", status, r)
	}
	var email *string
	if err := pool.QueryRow(context.Background(), `SELECT email FROM app_user WHERE id = $1`, svc.id).Scan(&email); err != nil || email != nil {
		t.Fatalf("the refused sign-in changed the account: email %v, %v", email, err)
	}
}

func TestAuditLogNamesTheServiceToken(t *testing.T) {
	w := newWorld(t, 35)
	lines := auditLog(t)
	as := access.Principal{UserID: "US-00000000000000000000000000", Admin: true, Service: true, TokenID: "TK-00000000000000000000000001"}
	if r, _ := exec(t, as, 0, createServiceAccount, map[string]any{"in": map[string]any{"name": "x"}}); len(r.Errors) != 1 {
		t.Fatalf("expected a refusal, got %+v", r.Errors)
	}
	if r, _ := exec(t, as, 0, updateAsset, map[string]any{"id": w.c1.asset, "in": map[string]any{"typeId": 1, "title": "x"}}); len(r.Errors) != 0 {
		t.Fatalf("update: %+v", r.Errors)
	}
	got := lines()
	if len(got) != 2 || got[0]["msg"] != "audit: denied" || got[1]["msg"] != "audit: change" {
		t.Fatalf("audit log = %v, want a refusal and a change", got)
	}
	for _, e := range got {
		if e["user"] != as.UserID || e["token"] != as.TokenID {
			t.Fatalf("audit line %v does not name the account and its token", e)
		}
	}
}

func TestServiceCLI(t *testing.T) {
	t.Setenv(config.EnvFileVar, filepath.Join(t.TempDir(), "missing.env"))
	t.Setenv("DATABASE_URL", dbURL)
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := cli.Run(context.Background(), args, &out)
		return out.String(), err
	}
	name := "cli-" + unique()
	token := regexp.MustCompile(`(?m)^mbs_[\w-]{43}$`)
	id := regexp.MustCompile(`TK-[0-9A-Z]{26}`)

	if out, err := run("service", "create", "-admin", name); err != nil || !strings.Contains(out, "administrator = true") {
		t.Fatalf("create: %q %v", out, err)
	}
	if _, err := run("service", "create", name); err == nil {
		t.Fatal("a second account with the same name must fail")
	}
	out, err := run("service", "token", "-name", "first", "-expires", "2d", name)
	if err != nil || !token.MatchString(out) || !strings.Contains(out, time.Now().Add(48*time.Hour).Format(time.DateOnly)) {
		t.Fatalf("token: %q %v", out, err)
	}
	first, firstID := token.FindString(out), id.FindString(out)
	if out, err = run("service", "token", name); err != nil || !token.MatchString(out) || !strings.Contains(out, `"cli"`) {
		t.Fatalf("token with defaults: %q %v", out, err)
	}
	second := token.FindString(out)
	if _, err := run("service", "token", "no-such-account"); err == nil || !strings.Contains(err.Error(), "service create") {
		t.Fatalf("unknown account: %v", err)
	}

	if out, err = run("service", "revoke", firstID); err != nil || !strings.Contains(out, "revoked") {
		t.Fatalf("revoke: %q %v", out, err)
	}
	if _, err := run("service", "revoke", "TK-00000000000000000000000000"); err == nil {
		t.Fatal("revoking an unknown token must fail")
	}
	out, err = run("service", "list")
	if err != nil || !strings.Contains(out, name+" (administrator = true, active = true)") ||
		!strings.Contains(out, `"first" revoked`) || !strings.Contains(out, `"cli" expires`) || strings.Contains(out, "mbs_") {
		t.Fatalf("list: %q %v", out, err)
	}

	// The tokens the command line printed are the ones the API accepts.
	if status, _ := post(t, map[string]string{"Authorization": "Bearer " + first}, `{ ping }`, nil); status != http.StatusUnauthorized {
		t.Fatalf("revoked token: status %d", status)
	}
	user{token: second}.must(t, `{ users { id } }`, nil, nil)
}

// The access scenario's service tokens are the same for the same seed, and they work.
func TestAccessScenarioServiceTokens(t *testing.T) {
	ctx := context.Background()
	url := freshDB(t, "devdata_tokens")
	s := newStore(t, url)
	a, err := s.Apply(ctx, "access", devdata.ApplyRequest{Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Apply(ctx, "access", devdata.ApplyRequest{Seed: 7})
	if err != nil || a.Summary != b.Summary {
		t.Fatalf("same seed, different result: %q / %q, %v", a.Summary, b.Summary, err)
	}
	tokens := regexp.MustCompile(`mbs_[\w-]{43}`).FindAllString(a.Summary, -1)
	if len(tokens) != 2 || rows(t, s, "service_token") != 4 {
		t.Fatalf("tokens in %q, %d rows", a.Summary, rows(t, s, "service_token"))
	}
	p, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	svc := access.NewService(sqlc.New(p), db.PoolTx(p))
	for i, admin := range []bool{true, false} {
		who, expires, err := svc.ServiceSignIn(ctx, tokens[i])
		if err != nil || who.Admin != admin || !who.Service || who.TokenID == "" || time.Until(expires) < 364*24*time.Hour {
			t.Fatalf("token %d: %+v, %v", i, who, err)
		}
	}
}
