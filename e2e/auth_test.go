//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bcc-media/mediabrain/internal/auth"
	"github.com/bcc-media/mediabrain/internal/auth/authtest"
	"github.com/bcc-media/mediabrain/internal/cli"
	"github.com/bcc-media/mediabrain/internal/config"
)

func TestRequestsNeedAValidToken(t *testing.T) {
	now := time.Now()
	expired := issuer.Sign(map[string]any{"iss": issuer.URL, "aud": authtest.Audience, "sub": "x", "exp": now.Add(-time.Hour).Unix()})
	other := authtest.NewIssuer()
	defer other.Close()
	for name, headers := range map[string]map[string]string{
		"no token":       nil,
		"garbage":        {"Authorization": "Bearer nope"},
		"expired":        {"Authorization": "Bearer " + expired},
		"foreign issuer": {"Authorization": "Bearer " + other.Token("x", nil)},
	} {
		status, r := post(t, headers, `{ ping }`, nil)
		if status != http.StatusUnauthorized || len(r.Errors) != 1 || r.Errors[0].Extensions.Code != "UNAUTHENTICATED" || len(r.Data) != 0 {
			t.Errorf("%s: status %d, body %+v", name, status, r)
		}
	}
}

func TestSignInCreatesAndUpdatesTheUser(t *testing.T) {
	u := signIn(t, "crew-a", "crew-b")
	var me struct {
		Me struct {
			ID, ExternalID, Email, Name string
			Active, IsAdmin             bool
		}
	}
	u.must(t, `{ me { id externalId email name active isAdmin } }`, nil, &me)
	if me.Me.ExternalID != u.sub || me.Me.Email != u.email || !me.Me.Active || me.Me.IsAdmin || !strings.HasPrefix(me.Me.ID, "US-") {
		t.Fatalf("me = %+v", me.Me)
	}
	groups := func() []string {
		rows, err := pool.Query(context.Background(),
			`SELECT g.name FROM user_group_member m JOIN user_group g ON g.id = m.group_id WHERE m.user_id = $1 AND m.source = 'sso' ORDER BY g.name`, u.id)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var n string
			_ = rows.Scan(&n)
			out = append(out, n)
		}
		return out
	}
	if got := groups(); strings.Join(got, ",") != "crew-a,crew-b" {
		t.Fatalf("groups after first sign-in = %v", got)
	}

	// A later token with a new name and other groups updates the same user.
	u.token = issuer.Token(u.sub, map[string]any{"email": u.email, "name": "Renamed", "groups": []string{"crew-b", "crew-c"}})
	u.must(t, `{ me { id name } }`, nil, &me)
	if me.Me.ID != u.id || me.Me.Name != "Renamed" {
		t.Fatalf("after update: %+v", me.Me)
	}
	if got := groups(); strings.Join(got, ",") != "crew-b,crew-c" {
		t.Fatalf("groups after second sign-in = %v", got)
	}
}

func TestATokenCannotTakeOverAnotherUsersEmail(t *testing.T) {
	victim := signIn(t)
	thief := user{sub: "e2e|thief-" + unique()}
	var me struct{ Me struct{ ID, Email *string } }
	for _, email := range []string{victim.email, strings.ToUpper(victim.email)} {
		thief.token = tokenFor(thief.sub, email)
		thief.must(t, `{ me { id email } }`, nil, &me)
		if *me.Me.ID == victim.id || me.Me.Email != nil {
			t.Fatalf("with %s the thief became %+v", email, me.Me)
		}
	}
}

func TestInactiveUsersAreRefused(t *testing.T) {
	u := signIn(t)
	if _, err := pool.Exec(context.Background(), `UPDATE app_user SET active = false WHERE id = $1`, u.id); err != nil {
		t.Fatal(err)
	}
	status, r := post(t, map[string]string{"Authorization": "Bearer " + tokenFor(u.sub, u.email)}, `{ ping }`, nil)
	if status != http.StatusForbidden || r.Errors[0].Extensions.Code != "FORBIDDEN" {
		t.Fatalf("inactive: %d %+v", status, r)
	}
	if status, _ := post(t, map[string]string{auth.DebugUserHeader: u.id}, `{ ping }`, nil); status != http.StatusForbidden {
		t.Fatalf("inactive via debug bypass: %d", status)
	}
}

func TestDebugBypass(t *testing.T) {
	u := signIn(t)
	for name, headers := range map[string]map[string]string{
		"header with id":    {auth.DebugUserHeader: u.id},
		"header with email": {auth.DebugUserHeader: u.email},
		"cookie":            {"Cookie": auth.DebugUserCookie + "=" + u.id},
	} {
		status, r := post(t, headers, `{ me { id } }`, nil)
		if status != http.StatusOK || !strings.Contains(string(r.Data), u.id) {
			t.Errorf("%s: %d %s %+v", name, status, r.Data, r.Errors)
		}
	}
	if status, _ := post(t, map[string]string{auth.DebugUserHeader: "nobody@example.test"}, `{ ping }`, nil); status != http.StatusUnauthorized {
		t.Fatalf("unknown debug user: %d", status)
	}
}

func TestAdministrators(t *testing.T) {
	u := signIn(t)
	admin := signInAdmin(t)
	const setAdmin = `mutation($u: String!, $a: Boolean!) { setUserAdmin(user: $u, admin: $a) { id isAdmin } }`

	u.fails(t, "FORBIDDEN", setAdmin, map[string]any{"u": u.id, "a": true})
	u.fails(t, "FORBIDDEN", `{ users { nodes { id } } }`, nil)
	u.fails(t, "FORBIDDEN", `{ userGroups { id } }`, nil)
	admin.fails(t, "NOT_FOUND", setAdmin, map[string]any{"u": "ghost@example.test", "a": true})

	var out struct{ SetUserAdmin struct{ IsAdmin bool } }
	admin.must(t, setAdmin, map[string]any{"u": u.email, "a": true}, &out)
	if !out.SetUserAdmin.IsAdmin {
		t.Fatal("not promoted")
	}
	u.token = tokenFor(u.sub, u.email)
	var list struct {
		Users      nodes[struct{ ID string }]
		UserGroups []struct{ ID string }
	}
	u.must(t, `{ users(first: 500) { nodes { id } } userGroups { id } }`, nil, &list)
	if len(list.Users) < 2 {
		t.Fatalf("users = %v", list.Users)
	}
	u.fails(t, "INVALID_INPUT", `{ users(first: 501) { nodes { id } } }`, nil)
}

func TestAdminCLI(t *testing.T) {
	u := signIn(t)
	t.Setenv(config.EnvFileVar, filepath.Join(t.TempDir(), "missing.env"))
	t.Setenv("DATABASE_URL", dbURL)
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := cli.Run(context.Background(), args, &out)
		return out.String(), err
	}
	isAdmin := func() bool {
		var b bool
		if err := pool.QueryRow(context.Background(), `SELECT is_admin FROM app_user WHERE id = $1`, u.id).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	if out, err := run("admin", "grant", u.email); err != nil || !strings.Contains(out, "administrator = true") || !isAdmin() {
		t.Fatalf("grant: %q %v", out, err)
	}
	if out, err := run("admin", "revoke", u.id); err != nil || !strings.Contains(out, "administrator = false") || isAdmin() {
		t.Fatalf("revoke: %q %v", out, err)
	}
	if _, err := run("admin", "grant", "never-signed-in@example.test"); err == nil || !strings.Contains(err.Error(), "sign in once") {
		t.Fatalf("unknown user: %v", err)
	}
}
