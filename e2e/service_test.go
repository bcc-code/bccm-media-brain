//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bcc-media/mediabrain/internal/auth"
	"github.com/bcc-media/mediabrain/internal/auth/authtest"
	"github.com/bcc-media/mediabrain/internal/cli"
	"github.com/bcc-media/mediabrain/internal/config"
)

const (
	createServiceAccount = `mutation($in: ServiceAccountInput!) { createServiceAccount(input: $in) { id kind externalId name email isAdmin active } }`
	setServiceActive     = `mutation($s: String!, $a: Boolean!) { setServiceAccountActive(serviceAccount: $s, active: $a) { id active } }`
	whoAmI               = `{ me { id kind externalId name } }`
)

type meJSON struct {
	Me struct{ ID, Kind, ExternalID, Name string }
}

// serviceToken is what the identity provider gives a machine client: the service group, and
// the username the account is created under in MediaBrain.
func serviceToken(sub, username string, groups ...string) string {
	return issuer.Token(sub, map[string]any{"preferred_username": username, "groups": append(groups, authtest.ServiceGroup)})
}

func newServiceAccount(t *testing.T, admin user, name string, isAdmin bool) string {
	t.Helper()
	var out struct{ CreateServiceAccount struct{ ID string } }
	admin.must(t, createServiceAccount, map[string]any{"in": map[string]any{"name": name, "admin": isAdmin}}, &out)
	return out.CreateServiceAccount.ID
}

// signInService creates a service account and makes its first request. sub is the account's
// name; the subject of its tokens is serviceSub(name).
func signInService(t *testing.T, admin bool) user {
	t.Helper()
	name := "svc-" + unique()
	u := user{id: newServiceAccount(t, signInAdmin(t), name, admin), sub: name, token: serviceToken(serviceSub(name), name)}
	var me meJSON
	u.must(t, whoAmI, nil, &me)
	if me.Me.ID != u.id {
		t.Fatalf("signed in as %+v, want %s", me.Me, u.id)
	}
	return u
}

func serviceSub(name string) string { return "machine|" + name }

// refused sends a token that nobody has used before and expects HTTP 403.
func refused(t *testing.T, why, token string) {
	t.Helper()
	status, r := post(t, map[string]string{"Authorization": "Bearer " + token}, `{ ping }`, nil)
	if status != http.StatusForbidden || len(r.Errors) != 1 || r.Errors[0].Extensions.Code != "FORBIDDEN" || len(r.Data) != 0 {
		t.Errorf("%s: status %d, body %+v", why, status, r)
	}
}

func usersWith(t *testing.T, externalID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM app_user WHERE external_id = $1`, externalID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestServiceAccounts(t *testing.T) {
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

	// The first token with that username is the account, and from then on its subject is.
	sub := serviceSub(name)
	lines := auditLog(t)
	var me meJSON
	user{token: serviceToken(sub, name)}.must(t, whoAmI, nil, &me)
	if me.Me.ID != a.ID || me.Me.Kind != "SERVICE" || me.Me.ExternalID != sub || me.Me.Name != name {
		t.Fatalf("first sign-in = %+v, want %s under %s", me.Me, a.ID, sub)
	}
	// Renamed at the identity provider: still the same account, still named as it was created.
	user{token: serviceToken(sub, "renamed-"+name)}.must(t, whoAmI, nil, &me)
	if me.Me.ID != a.ID || me.Me.Name != name {
		t.Fatalf("after a rename = %+v", me.Me)
	}
	// Which subject took the account is on record, once.
	if got := lines(); len(got) != 1 || got[0]["action"] != "service_account.first_sign_in" || got[0]["user"] != a.ID || got[0]["subject"] != sub {
		t.Fatalf("audit log = %v, want the first sign-in only", got)
	}
	// Somebody else who takes the old username does not get the account.
	refused(t, "another subject under the same username", serviceToken("machine|impostor-"+unique(), name))
	// The name stays taken although the external id no longer holds it.
	admin.fails(t, "CONFLICT", createServiceAccount, map[string]any{"in": map[string]any{"name": name}})

	var list struct {
		ServiceAccounts []struct{ ID, Kind string }
	}
	admin.must(t, `{ serviceAccounts { id kind } }`, nil, &list)
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
}

// Nobody becomes a service account by holding a valid token: the account must have been
// created, and a refused token leaves nothing behind.
func TestRefusedServiceAccounts(t *testing.T) {
	admin := signInAdmin(t)

	unknown := "machine|unknown-" + unique()
	refused(t, "no such account", serviceToken(unknown, "nobody-made-"+unique()))
	refused(t, "no username", issuer.Token(unknown, map[string]any{"groups": []string{authtest.ServiceGroup}}))
	if n := usersWith(t, unknown); n != 0 {
		t.Fatalf("a refused service token created %d user(s)", n)
	}

	stranger := "e2e|stranger-" + unique()
	refused(t, "neither group", issuer.Token(stranger, map[string]any{"groups": []string{"somewhere-else"}}))
	refused(t, "no groups", issuer.Token(stranger, map[string]any{"groups": nil}))
	if n := usersWith(t, stranger); n != 0 {
		t.Fatalf("a token without a group created %d user(s)", n)
	}

	// Someone who signed in as a person is not a service account because their token now says so.
	person := signIn(t)
	refused(t, "a person with the service group", serviceToken(person.sub, "whoever"))
	// Nor after an account was made under their username: it is another subject's to take.
	personName := "person-" + unique()
	newServiceAccount(t, admin, personName, true)
	refused(t, "a person under a new account's username", serviceToken(person.sub, personName))
	var kind string
	if err := pool.QueryRow(context.Background(), `SELECT kind FROM app_user WHERE id = $1`, person.id).Scan(&kind); err != nil || kind != "person" {
		t.Fatalf("the refused sign-ins changed the person: kind %q, %v", kind, err)
	}

	// A service account does not get in as a person, who would need no account.
	svc := signInService(t, false)
	refused(t, "a service account without the service group", issuer.Token(serviceSub(svc.sub), map[string]any{"preferred_username": svc.sub}))

	var set struct{ SetServiceAccountActive struct{ Active bool } }
	admin.must(t, setServiceActive, map[string]any{"s": svc.sub, "a": false}, &set)
	if set.SetServiceAccountActive.Active {
		t.Fatal("still active")
	}
	refused(t, "switched off", serviceToken(serviceSub(svc.sub), svc.sub))
	admin.must(t, setServiceActive, map[string]any{"s": svc.id, "a": true}, nil)
	user{token: serviceToken(serviceSub(svc.sub), svc.sub)}.must(t, `{ me { id } }`, nil, nil)

	// An account that has not signed in yet can be switched off too, and then cannot start.
	pending := "pending-" + unique()
	newServiceAccount(t, admin, pending, false)
	admin.must(t, setServiceActive, map[string]any{"s": pending, "a": false}, nil)
	refused(t, "switched off before its first sign-in", serviceToken(serviceSub(pending), pending))

	admin.fails(t, "NOT_FOUND", setServiceActive, map[string]any{"s": admin.id, "a": false})
	admin.fails(t, "NOT_FOUND", setServiceActive, map[string]any{"s": "no-such-account", "a": false})
}

// An account that signed in as a person before the groups were sorted out is made a service
// account in place, keeping its id and what it was granted.
func TestMakeAPersonAServiceAccount(t *testing.T) {
	w := newWorld(t, 63)
	person := signIn(t)
	grant(t, w.admin, "PROJECT", w.projectA, "VIEW", to(person))
	name := "flows-" + unique()

	vars := func(user string) map[string]any {
		return map[string]any{"in": map[string]any{"name": name, "admin": true, "user": user}}
	}
	w.admin.fails(t, "NOT_FOUND", createServiceAccount, vars("US-00000000000000000000000000"))
	w.admin.fails(t, "INVALID_INPUT", createServiceAccount, vars(w.admin.id))
	var acc struct {
		CreateServiceAccount struct {
			ID, Kind, ExternalID, Name string
			IsAdmin                    bool
		}
	}
	w.admin.must(t, createServiceAccount, vars(person.id), &acc)
	a := acc.CreateServiceAccount
	if a.ID != person.id || a.Kind != "SERVICE" || a.ExternalID != person.sub || a.Name != name || !a.IsAdmin {
		t.Fatalf("account = %+v", a)
	}
	// It is one already, and its name is taken.
	w.admin.fails(t, "NOT_FOUND", createServiceAccount, vars(person.id))
	w.admin.fails(t, "CONFLICT", createServiceAccount, map[string]any{"in": map[string]any{"name": name, "user": signIn(t).id}})

	svc := user{id: person.id, token: serviceToken(person.sub, "any-username")}
	var me meJSON
	svc.must(t, whoAmI, nil, &me)
	if me.Me.ID != person.id || me.Me.Kind != "SERVICE" {
		t.Fatalf("me = %+v", me.Me)
	}
	svc.sees(t, "asset", w.c1.asset)
	refused(t, "the token of a person, for what is now a service account", tokenFor(person.sub, person.email))
}

func TestServiceAccountsUseGrants(t *testing.T) {
	w := newWorld(t, 61)
	name := "reader-" + unique()
	crew := "crew-" + unique()
	id := newServiceAccount(t, w.admin, name, false)
	// Given before the account has ever signed in.
	grant(t, w.admin, "ASSET", w.c3.asset, "VIEW", to(user{id: id}))

	svc := user{id: id, token: serviceToken(serviceSub(name), name, crew)}
	svc.sees(t, "asset", w.c3.asset)
	svc.isNull(t, "asset", w.c1.asset)
	svc.fails(t, "FORBIDDEN", `{ files { nodes { id } } }`, nil)

	grant(t, w.admin, "PROJECT", w.projectA, "VIEW", to(svc))
	svc.sees(t, "asset", w.c1.asset)
	svc.fails(t, "FORBIDDEN", updateAsset, map[string]any{"id": w.c1.asset, "in": map[string]any{"typeId": 1, "title": "x"}})

	// Its groups at the identity provider are groups here, like a person's.
	var groupID string
	if err := pool.QueryRow(context.Background(), `SELECT g.id FROM user_group g JOIN user_group_member m ON m.group_id = g.id
		WHERE g.name = $1 AND m.user_id = $2`, crew, id).Scan(&groupID); err != nil {
		t.Fatalf("the service account is not in its group %q: %v", crew, err)
	}
	var out any
	w.admin.must(t, `mutation($in: GrantInput!) { grantAccess(input: $in) { id } }`, map[string]any{"in": map[string]any{
		"scope": "ASSET", "scopeId": w.c1.asset, "groupId": groupID, "level": "EDIT"}}, &out)
	svc.must(t, updateAsset, map[string]any{"id": w.c1.asset, "in": map[string]any{"typeId": 1, "title": w.marker + " by a machine"}}, nil)
}

// An administrator service account does what administrators do, except what would let a
// leaked credential outlive its withdrawal: service accounts and administrators.
func TestServiceAccountsCannotManageAccounts(t *testing.T) {
	w := newWorld(t, 62)
	svc := signInService(t, true)
	other := signInService(t, false)
	person := signIn(t)

	svc.must(t, `{ users { nodes { id } } files { nodes { id } } }`, nil, nil)
	svc.must(t, registerFile, map[string]any{"in": fileVars(w.f.VideoFile())}, nil)

	denied := map[string]struct {
		query string
		vars  map[string]any
	}{
		"create account": {createServiceAccount, map[string]any{"in": map[string]any{"name": "x" + unique()}}},
		"make account":   {createServiceAccount, map[string]any{"in": map[string]any{"name": "x" + unique(), "user": person.id}}},
		"deactivate":     {setServiceActive, map[string]any{"s": other.id, "a": false}},
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
		t.Fatalf("debug bypass as a service account may manage accounts: %+v", r.Errors)
	}
}

// An account that has not signed in yet is stored under "service|<name>". A token whose
// subject is that text is not it, with either group.
func TestSignInCannotBecomeAServiceAccount(t *testing.T) {
	admin := signInAdmin(t)
	name := "waiting-" + unique()
	id := newServiceAccount(t, admin, name, true)
	refused(t, "as a person", tokenFor("service|"+name, "thief-"+unique()+"@example.test"))
	refused(t, "as a service account", serviceToken("service|"+name, "someone-else"))
	var email *string
	var external string
	err := pool.QueryRow(context.Background(), `SELECT email, external_id FROM app_user WHERE id = $1`, id).Scan(&email, &external)
	if err != nil || email != nil || external != "service|"+name {
		t.Fatalf("the refused sign-ins changed the account: email %v, external id %q, %v", email, external, err)
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

	if out, err := run("service", "create", "-admin", name); err != nil || !strings.Contains(out, "administrator = true") {
		t.Fatalf("create: %q %v", out, err)
	}
	if _, err := run("service", "create", name); err == nil {
		t.Fatal("a second account with the same name must fail")
	}
	out, err := run("service", "list")
	if err != nil || !strings.Contains(out, name+" (administrator = true, active = true, has not signed in yet)") {
		t.Fatalf("list: %q %v", out, err)
	}

	// The account the command line made is the one the API lets in.
	user{token: serviceToken(serviceSub(name), name)}.must(t, `{ users { nodes { id } } }`, nil, nil)
	if out, err = run("service", "list"); err != nil || !strings.Contains(out, name+" (administrator = true, active = true, signed in)") {
		t.Fatalf("list after the first sign-in: %q %v", out, err)
	}

	person := signIn(t)
	made := "cli-made-" + unique()
	if _, err := run("service", "create", "-user", "US-00000000000000000000000000", made); err == nil || !strings.Contains(err.Error(), "signed in as a person") {
		t.Fatalf("unknown user: %v", err)
	}
	if out, err = run("service", "create", "-user", person.id, made); err != nil || !strings.HasPrefix(out, person.id+": service account") {
		t.Fatalf("create -user: %q %v", out, err)
	}
	var me meJSON
	user{token: serviceToken(person.sub, "x")}.must(t, whoAmI, nil, &me)
	if me.Me.ID != person.id || me.Me.Kind != "SERVICE" || me.Me.Name != made {
		t.Fatalf("me = %+v", me.Me)
	}
}
