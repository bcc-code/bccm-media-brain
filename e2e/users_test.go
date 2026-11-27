//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
)

const (
	listUsers = `query($f: UserFilter, $first: Int, $after: String) {
		users(filter: $f, first: $first, after: $after) { nodes { id kind email groups { name } } pageInfo { hasNextPage endCursor } }
	}`
	setUserActive = `mutation($u: String!, $a: Boolean!) { setUserActive(user: $u, active: $a) { id active } }`
)

type userList struct {
	Users struct {
		Nodes []struct {
			ID     string
			Kind   string
			Email  *string
			Groups []struct{ Name string }
		}
		PageInfo struct {
			HasNextPage bool
			EndCursor   *string
		}
	}
}

func (l userList) ids() []string {
	var out []string
	for _, n := range l.Users.Nodes {
		out = append(out, n.ID)
	}
	return out
}

func TestUserSearchAndFilter(t *testing.T) {
	admin := signInAdmin(t)
	crew := "crew-" + unique()
	person := signIn(t, crew)
	marker := strings.TrimPrefix(person.sub, "e2e|")

	var l userList
	admin.must(t, listUsers, map[string]any{"f": map[string]any{"search": "  " + strings.ToUpper(marker) + " "}}, &l)
	if got := l.ids(); !slices.Equal(got, []string{person.id}) {
		t.Fatalf("search by part of the email in another case = %v, want %s", got, person.id)
	}
	if !slices.ContainsFunc(l.Users.Nodes[0].Groups, func(g struct{ Name string }) bool { return g.Name == crew }) {
		t.Fatalf("groups = %+v, want %s", l.Users.Nodes[0].Groups, crew)
	}

	// Name matches too: tokens name the user "Test <sub>".
	admin.must(t, listUsers, map[string]any{"f": map[string]any{"search": "test " + person.sub}}, &l)
	if got := l.ids(); !slices.Equal(got, []string{person.id}) {
		t.Fatalf("search by name = %v", got)
	}

	// Kind and paging, with a filter that matches three service accounts.
	page := "page-" + unique()
	var svcs []string
	for i := range 3 {
		svcs = append(svcs, newServiceAccount(t, admin, page+"-"+string(rune('a'+i)), false))
	}
	admin.must(t, listUsers, map[string]any{"f": map[string]any{"search": page, "kind": "PERSON"}}, &l)
	if len(l.Users.Nodes) != 0 {
		t.Fatalf("people named like service accounts: %v", l.ids())
	}
	var got []string
	var after any
	for {
		admin.must(t, listUsers, map[string]any{"f": map[string]any{"search": page, "kind": "SERVICE"}, "first": 2, "after": after}, &l)
		got = append(got, l.ids()...)
		if !l.Users.PageInfo.HasNextPage {
			break
		}
		after = *l.Users.PageInfo.EndCursor
	}
	slices.Sort(svcs)
	if !slices.Equal(got, svcs) {
		t.Fatalf("paged service accounts = %v, want %v", got, svcs)
	}

	// An empty search is no filter.
	admin.must(t, listUsers, map[string]any{"f": map[string]any{"search": " "}, "first": 2}, &l)
	if len(l.Users.Nodes) != 2 {
		t.Fatalf("blank search = %v", l.ids())
	}

	person.fails(t, "FORBIDDEN", listUsers, map[string]any{"f": map[string]any{"search": marker}})
}

func TestUserGroupsAreOwnOrAdmins(t *testing.T) {
	crew := "crew-" + unique()
	u := signIn(t, crew)
	other := signIn(t, "crew-"+unique())

	var me struct {
		Me struct{ Groups []struct{ Name string } }
	}
	u.must(t, `{ me { groups { name } } }`, nil, &me)
	if !slices.ContainsFunc(me.Me.Groups, func(g struct{ Name string }) bool { return g.Name == crew }) {
		t.Fatalf("own groups = %+v", me.Me.Groups)
	}

	svc := access.NewService(sqlc.New(pool), db.PoolTx(pool))
	ctx := access.With(context.Background(), access.Principal{UserID: u.id})
	groups, err := svc.GroupsFor(ctx, []string{u.id, other.id})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups[u.id]) == 0 || groups[other.id] == nil || len(groups[other.id]) != 0 {
		t.Fatalf("a user who is not an administrator sees %+v", groups)
	}
	if _, err := svc.GroupsFor(context.Background(), []string{u.id}); err == nil {
		t.Fatal("groups without a principal")
	}
}

func TestSetUserActive(t *testing.T) {
	admin := signInAdmin(t)
	u := signIn(t)
	lines := auditLog(t)

	var out struct{ SetUserActive struct{ Active bool } }
	admin.must(t, setUserActive, map[string]any{"u": u.email, "a": false}, &out)
	if out.SetUserActive.Active {
		t.Fatal("still active")
	}
	// A new token, because the principal of the old one is cached for a minute.
	if status, _ := post(t, map[string]string{"Authorization": "Bearer " + tokenFor(u.sub, u.email, "fresh-"+unique())}, `{ ping }`, nil); status != http.StatusForbidden {
		t.Fatalf("switched off, then %d", status)
	}
	admin.must(t, setUserActive, map[string]any{"u": u.id, "a": true}, &out)
	if !out.SetUserActive.Active {
		t.Fatal("not switched on")
	}
	u.token = tokenFor(u.sub, u.email, "again-"+unique())
	u.must(t, `{ ping }`, nil, nil)

	var actions []string
	for _, l := range lines() {
		if l["target"] == u.id {
			actions = append(actions, l["action"].(string))
		}
	}
	if !slices.Equal(actions, []string{"user.set_active", "user.set_active"}) {
		t.Fatalf("audit = %v", actions)
	}

	// Not your own account, not a service account, not someone missing.
	if e := admin.fails(t, "INVALID_INPUT", setUserActive, map[string]any{"u": admin.id, "a": false}); e.Extensions.Field != "user" {
		t.Fatalf("own account refused on %q", e.Extensions.Field)
	}
	admin.must(t, setUserActive, map[string]any{"u": admin.id, "a": true}, nil)
	machine := newServiceAccount(t, admin, "svc-"+unique(), false)
	admin.fails(t, "NOT_FOUND", setUserActive, map[string]any{"u": machine, "a": false})
	admin.fails(t, "NOT_FOUND", setUserActive, map[string]any{"u": "ghost-" + unique() + "@example.test", "a": false})

	// Only administrators that are people.
	u.fails(t, "FORBIDDEN", setUserActive, map[string]any{"u": admin.id, "a": false})
	signInService(t, true).fails(t, "FORBIDDEN", setUserActive, map[string]any{"u": u.id, "a": false})
}
