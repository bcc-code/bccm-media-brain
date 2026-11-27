package access

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
)

// Service manages users, groups, grants and service accounts.
type Service struct {
	q  sqlc.Querier
	tx db.TxRunner
}

func NewService(q sqlc.Querier, tx db.TxRunner) *Service {
	return &Service{q: q, tx: tx}
}

// Identity is what the auth proxy's token says about the caller.
type Identity struct {
	Subject string
	// Username is the identity provider's name for the account. Only a service account's
	// first sign-in reads it; Subject is the key everywhere else.
	Username string
	Email    string
	Name     string
	Groups   []string
	// Service says the token is a machine client's: it carries the service group.
	Service bool
	// Expires is when the proof of this identity (the token) stops being accepted. Whoever
	// remembers the identity must forget it by then. Zero: it may not be remembered at all.
	Expires time.Time
}

// SignIn returns the principal of a verified identity and syncs its SSO groups. A person is
// created or updated from the token. A service account is never created here: it must exist.
func (s *Service) SignIn(ctx context.Context, id Identity) (Principal, error) {
	if strings.TrimSpace(id.Subject) == "" {
		return Principal{}, domain.ErrUnauthenticated
	}
	// A service account that has not signed in yet is stored under this prefix, so a token
	// with such a subject would become it without the check of its username.
	if strings.HasPrefix(id.Subject, serviceSubject) {
		return Principal{}, fmt.Errorf("%w: the subject %q is reserved", domain.ErrForbidden, id.Subject)
	}
	var p Principal
	var first bool
	err := s.tx.InTx(ctx, func(q sqlc.Querier) error {
		var u sqlc.AppUser
		var err error
		if first = false; id.Service {
			u, first, err = serviceAccountOf(ctx, q, id)
		} else {
			u, err = person(ctx, q, id)
		}
		if err != nil {
			return err
		}
		if !u.Active {
			return fmt.Errorf("%w: %s is switched off", domain.ErrForbidden, u.ID)
		}
		keep := []string{}
		for _, name := range id.Groups {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			g, err := q.UpsertSSOGroup(ctx, sqlc.UpsertSSOGroupParams{Name: name, ExternalID: name})
			if err != nil {
				return domain.FromPG(err)
			}
			if err := q.AddGroupMember(ctx, sqlc.AddGroupMemberParams{GroupID: g.ID, UserID: u.ID, Source: sqlc.MembershipSourceSso}); err != nil {
				return err
			}
			keep = append(keep, g.ID)
		}
		if err := q.RemoveSSOMemberships(ctx, sqlc.RemoveSSOMembershipsParams{UserID: u.ID, Keep: keep}); err != nil {
			return err
		}
		p, err = principalOf(ctx, q, u)
		return err
	})
	if err == nil && first {
		// From now on this subject is the account, whatever username its tokens carry.
		_ = Audited(With(ctx, p), nil, "service_account.first_sign_in", p.UserID, "subject", id.Subject)
	}
	return p, err
}

func person(ctx context.Context, q sqlc.Querier, id Identity) (sqlc.AppUser, error) {
	email := domain.Blank(&id.Email)
	if email != nil {
		// email is unique; a token cannot take over the address of another account.
		other, err := q.GetUserByEmail(ctx, *email)
		if err == nil && other.ExternalID != id.Subject {
			email = nil
		} else if err != nil && !errors.Is(domain.FromPG(err), domain.ErrNotFound) {
			return sqlc.AppUser{}, err
		}
	}
	u, err := q.UpsertUser(ctx, sqlc.UpsertUserParams{ExternalID: id.Subject, Email: email, Name: domain.Blank(&id.Name)})
	if err != nil {
		return sqlc.AppUser{}, err
	}
	// The transaction is rolled back, so the upsert above did not change the account.
	if u.Kind == sqlc.UserKindService {
		return sqlc.AppUser{}, fmt.Errorf("%w: %s is a service account, and the token lacks the service group", domain.ErrForbidden, u.ID)
	}
	return u, nil
}

// serviceAccountOf finds the service account of a token: by its subject, or at its first
// sign-in by the username it was created under, which binds it to the subject. It reports
// whether this was that first sign-in.
func serviceAccountOf(ctx context.Context, q sqlc.Querier, id Identity) (sqlc.AppUser, bool, error) {
	for attempt := 0; ; attempt++ {
		u, err := q.GetUserByExternalID(ctx, id.Subject)
		if err == nil {
			if u.Kind != sqlc.UserKindService {
				return sqlc.AppUser{}, false, fmt.Errorf("%w: %s signed in as a person before; an administrator has to make it a service account", domain.ErrForbidden, u.ID)
			}
			return u, false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return sqlc.AppUser{}, false, err
		}
		username := strings.TrimSpace(id.Username)
		if username == "" || attempt > 0 {
			return sqlc.AppUser{}, false, fmt.Errorf("%w: no service account %q", domain.ErrForbidden, username)
		}
		u, err = q.BindServiceAccount(ctx, sqlc.BindServiceAccountParams{ExternalID: id.Subject, Pending: serviceSubject + username})
		if err == nil {
			return u, true, nil
		}
		// No row: there is no such account, or a request at the same moment has just bound
		// it, which the second look by subject finds.
		if !errors.Is(err, pgx.ErrNoRows) {
			return sqlc.AppUser{}, false, domain.FromPG(err)
		}
	}
}

// Impersonate resolves a user by id or email without a token. Debug mode only.
func (s *Service) Impersonate(ctx context.Context, ref string) (Principal, error) {
	u, err := s.findUser(ctx, s.q, ref)
	if err != nil {
		return Principal{}, err
	}
	if !u.Active {
		return Principal{}, domain.ErrForbidden
	}
	return principalOf(ctx, s.q, u)
}

// DebugAdmin returns the user with the given email, creating them as an administrator when
// they do not exist. Debug mode only: it is what makes a fresh or freshly reset database usable.
func (s *Service) DebugAdmin(ctx context.Context, email string) (Principal, error) {
	p, err := s.Impersonate(ctx, email)
	if !errors.Is(err, domain.ErrNotFound) {
		return p, err
	}
	_, err = s.q.CreateUser(ctx, sqlc.CreateUserParams{
		ExternalID: "debug|" + email, Email: &email, Name: &email, IsAdmin: true, Active: true,
	})
	// A conflict means a parallel request created the user first.
	if err = domain.FromPG(err); err != nil && !errors.Is(err, domain.ErrConflict) {
		return Principal{}, err
	}
	return s.Impersonate(ctx, email)
}

func principalOf(ctx context.Context, q sqlc.Querier, u sqlc.AppUser) (Principal, error) {
	groups, err := q.ListUserGroupIDs(ctx, u.ID)
	if err != nil {
		return Principal{}, err
	}
	return Principal{UserID: u.ID, GroupIDs: groups, Admin: u.IsAdmin, Service: u.Kind == sqlc.UserKindService}, nil
}

func (s *Service) findUser(ctx context.Context, q sqlc.Querier, ref string) (sqlc.AppUser, error) {
	var u sqlc.AppUser
	var err error
	if strings.HasPrefix(ref, "US-") {
		u, err = q.GetUser(ctx, ref)
	} else {
		u, err = q.GetUserByEmail(ctx, ref)
	}
	return u, domain.FromPG(err)
}

func (s *Service) Me(ctx context.Context) (sqlc.AppUser, error) {
	p, err := From(ctx)
	if err != nil {
		return sqlc.AppUser{}, err
	}
	u, err := s.q.GetUser(ctx, p.UserID)
	return u, domain.FromPG(err)
}

type UserFilter struct {
	Kind *sqlc.UserKind
	// Search is part of the name or email, in any case.
	Search *string
}

func (s *Service) Users(ctx context.Context, f UserFilter, first int, after *string) (domain.Slice[sqlc.AppUser], error) {
	if err := RequireAdmin(ctx); err != nil {
		return domain.Slice[sqlc.AppUser]{}, err
	}
	w, err := domain.NewWindow(first, after, domain.DefaultLimit, domain.MaxLimit)
	if err != nil {
		return domain.Slice[sqlc.AppUser]{}, err
	}
	var search *string
	if f.Search != nil {
		if t := strings.TrimSpace(*f.Search); t != "" {
			search = &t
		}
	}
	rows, err := s.q.ListUsers(ctx, sqlc.ListUsersParams{Kind: f.Kind, Search: search, Limit: w.Fetch(), After: w.After})
	return domain.Cut(rows, w, func(u sqlc.AppUser) string { return u.ID }), err
}

// GroupsFor returns the groups of each user. Others' groups are for administrators: anyone
// else gets only their own, as an empty list for the rest, since a user can show up where
// its id is known (me, createdBy) without the caller being allowed to list users.
func (s *Service) GroupsFor(ctx context.Context, userIDs []string) (map[string][]sqlc.UserGroup, error) {
	p, err := From(ctx)
	if err != nil {
		return nil, err
	}
	ids := userIDs
	if !p.Admin {
		ids = nil
		for _, id := range userIDs {
			if id == p.UserID {
				ids = []string{id}
			}
		}
	}
	out := make(map[string][]sqlc.UserGroup, len(userIDs))
	for _, id := range userIDs {
		out[id] = []sqlc.UserGroup{}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q.ListGroupsForUsers(ctx, ids)
	for _, r := range rows {
		out[r.UserID] = append(out[r.UserID], r.UserGroup)
	}
	return out, err
}

func (s *Service) Groups(ctx context.Context) ([]sqlc.UserGroup, error) {
	if err := RequireAdmin(ctx); err != nil {
		return nil, err
	}
	return s.q.ListUserGroups(ctx)
}

// SetAdmin makes the user (id or email) a system administrator, or removes that.
func (s *Service) SetAdmin(ctx context.Context, ref string, admin bool) (sqlc.AppUser, error) {
	if err := RequirePersonAdmin(ctx); err != nil {
		return sqlc.AppUser{}, err
	}
	u, err := s.findUser(ctx, s.q, ref)
	if err != nil {
		return sqlc.AppUser{}, err
	}
	u, err = s.q.SetUserAdmin(ctx, sqlc.SetUserAdminParams{ID: u.ID, IsAdmin: admin})
	return u, Audited(ctx, domain.FromPG(err), "user.set_admin", u.ID, "admin", admin)
}

// SetActive switches a person (id or email) off or on. Service accounts have
// SetServiceAccountActive. Administrators cannot switch themselves off: nobody could undo it
// through the API if they were the last one.
func (s *Service) SetActive(ctx context.Context, ref string, active bool) (sqlc.AppUser, error) {
	if err := RequirePersonAdmin(ctx); err != nil {
		return sqlc.AppUser{}, err
	}
	u, err := s.findUser(ctx, s.q, ref)
	if err != nil {
		return sqlc.AppUser{}, err
	}
	if u.Kind != sqlc.UserKindPerson {
		return sqlc.AppUser{}, domain.ErrNotFound
	}
	if p, _ := From(ctx); p.UserID == u.ID && !active {
		return sqlc.AppUser{}, domain.InvalidField("user", "you cannot switch off your own account")
	}
	u, err = s.q.SetPersonActive(ctx, sqlc.SetPersonActiveParams{ID: u.ID, Active: active})
	return u, Audited(ctx, domain.FromPG(err), "user.set_active", u.ID, "active", active)
}

type UserInput struct {
	ExternalID string
	Email      string
	Name       string
	Admin      bool
	Inactive   bool
}

// CreateUser adds a user that has not signed in yet. Used by dev data and the debug UI.
func (s *Service) CreateUser(ctx context.Context, in UserInput) (sqlc.AppUser, error) {
	if err := RequireAdmin(ctx); err != nil {
		return sqlc.AppUser{}, err
	}
	ext, err := domain.Required("externalId", in.ExternalID)
	if err != nil {
		return sqlc.AppUser{}, err
	}
	u, err := s.q.CreateUser(ctx, sqlc.CreateUserParams{
		ExternalID: ext, Email: domain.Blank(&in.Email), Name: domain.Blank(&in.Name),
		IsAdmin: in.Admin, Active: !in.Inactive,
	})
	return u, Audited(ctx, domain.FromPG(err), "user.create", u.ID, "admin", in.Admin, "active", !in.Inactive)
}

// CreateGroup adds a local group with the given members.
func (s *Service) CreateGroup(ctx context.Context, name string, memberIDs []string) (sqlc.UserGroup, error) {
	if err := RequireAdmin(ctx); err != nil {
		return sqlc.UserGroup{}, err
	}
	name, err := domain.Required("name", name)
	if err != nil {
		return sqlc.UserGroup{}, err
	}
	var g sqlc.UserGroup
	err = s.tx.InTx(ctx, func(q sqlc.Querier) error {
		if g, err = q.CreateUserGroup(ctx, sqlc.CreateUserGroupParams{Name: name}); err != nil {
			return domain.FromPG(err)
		}
		for _, id := range memberIDs {
			err := q.AddGroupMember(ctx, sqlc.AddGroupMemberParams{GroupID: g.ID, UserID: id, Source: sqlc.MembershipSourceManual})
			if err != nil {
				return domain.FromPG(err)
			}
		}
		return nil
	})
	return g, Audited(ctx, err, "group.create", g.ID, "name", name, "members", memberIDs)
}

type GrantInput struct {
	Scope     sqlc.GrantScope
	ScopeID   string
	UserID    *string
	UserEmail *string
	GroupID   *string
	Level     sqlc.PermissionLevel
	ExpiresAt *time.Time
}

// requireOn checks the caller's level on the object a grant points at.
func requireOn(ctx context.Context, q sqlc.Querier, scope sqlc.GrantScope, id string, need sqlc.PermissionLevel) error {
	var have sqlc.PermissionLevel
	switch scope {
	case sqlc.GrantScopeAsset:
		if _, err := q.GetAsset(ctx, id); err != nil {
			return domain.FromPG(err)
		}
		l, err := AssetLevel(ctx, q, id)
		if err != nil {
			return err
		}
		have = l
	case sqlc.GrantScopeProject:
		pj, err := q.GetProject(ctx, id)
		if err != nil {
			return domain.FromPG(err)
		}
		if have, err = ProjectLevel(ctx, q, pj); err != nil {
			return err
		}
	case sqlc.GrantScopeProgram:
		if _, err := q.GetProgram(ctx, id); err != nil {
			return domain.FromPG(err)
		}
		l, err := ProgramLevel(ctx, q, id)
		if err != nil {
			return err
		}
		have = l
	default:
		return domain.Invalid("scope must be program, project or asset")
	}
	return Decide(have, need)
}

func (s *Service) Grant(ctx context.Context, in GrantInput) (sqlc.PermissionGrant, error) {
	p, err := From(ctx)
	if err != nil {
		return sqlc.PermissionGrant{}, err
	}
	if _, ok := rank[in.Level]; !ok {
		return sqlc.PermissionGrant{}, domain.InvalidField("level", "unknown level")
	}
	targets := 0
	for _, t := range []*string{in.UserID, in.UserEmail, in.GroupID} {
		if t != nil {
			targets++
		}
	}
	if targets != 1 {
		return sqlc.PermissionGrant{}, domain.Invalid("give exactly one of userId, userEmail or groupId")
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now()) {
		return sqlc.PermissionGrant{}, domain.InvalidField("expiresAt", "expiresAt must be in the future")
	}
	var g sqlc.PermissionGrant
	err = s.tx.InTx(ctx, func(q sqlc.Querier) error {
		if err := requireOn(ctx, q, in.Scope, in.ScopeID, Admin); err != nil {
			return err
		}
		userID := in.UserID
		if in.UserEmail != nil {
			u, err := q.GetUserByEmail(ctx, *in.UserEmail)
			if err != nil {
				if errors.Is(domain.FromPG(err), domain.ErrNotFound) {
					return domain.Invalid("no user with that email has signed in yet")
				}
				return err
			}
			userID = &u.ID
		}
		var err error
		g, err = q.CreateGrant(ctx, sqlc.CreateGrantParams{
			UserID: userID, GroupID: in.GroupID, Scope: in.Scope, ScopeID: in.ScopeID,
			Level: in.Level, GrantedBy: p.UserRef(), ExpiresAt: in.ExpiresAt,
		})
		if err != nil {
			return domain.FromPG(err)
		}
		return RefreshScope(ctx, q, in.Scope, in.ScopeID)
	})
	return g, Audited(ctx, err, "grant.create", g.ID, grantAttrs(g)...)
}

func (s *Service) Revoke(ctx context.Context, grantID string) error {
	var g sqlc.PermissionGrant
	err := s.tx.InTx(ctx, func(q sqlc.Querier) error {
		var err error
		g, err = q.GetGrant(ctx, grantID)
		if err != nil {
			return domain.FromPG(err)
		}
		if err := requireOn(ctx, q, g.Scope, g.ScopeID, Admin); err != nil {
			return err
		}
		if _, err := q.DeleteGrant(ctx, grantID); err != nil {
			return err
		}
		return RefreshScope(ctx, q, g.Scope, g.ScopeID)
	})
	return Audited(ctx, err, "grant.revoke", g.ID, grantAttrs(g)...)
}

// grantAttrs describes a grant in an audit record: on what, to whom, how much and until when.
func grantAttrs(g sqlc.PermissionGrant) []any {
	// Not "level": that key is the log record's own severity.
	attrs := []any{"scope", string(g.Scope), "scope_id", g.ScopeID, "permission", string(g.Level)}
	if g.UserID != nil {
		attrs = append(attrs, "to_user", *g.UserID)
	}
	if g.GroupID != nil {
		attrs = append(attrs, "to_group", *g.GroupID)
	}
	if g.ExpiresAt != nil {
		attrs = append(attrs, "expires_at", g.ExpiresAt.UTC())
	}
	return attrs
}

func (s *Service) Grants(ctx context.Context, scope sqlc.GrantScope, id string) ([]sqlc.PermissionGrant, error) {
	if err := requireOn(ctx, s.q, scope, id, Admin); err != nil {
		return nil, err
	}
	return s.q.ListGrants(ctx, sqlc.ListGrantsParams{Scope: scope, ScopeID: id})
}

// Sweep removes expired grants and the access they gave. It returns how many were removed.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	n := 0
	err := s.tx.InTx(ctx, func(q sqlc.Querier) error {
		expired, err := q.DeleteExpiredGrants(ctx)
		if err != nil {
			return err
		}
		n = len(expired)
		for _, g := range expired {
			if err := RefreshScope(ctx, q, g.Scope, g.ScopeID); err != nil {
				return err
			}
		}
		return nil
	})
	return n, err
}
