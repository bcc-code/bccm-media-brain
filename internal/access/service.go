package access

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
)

// Service manages users, groups, grants, service accounts and their tokens.
type Service struct {
	q  sqlc.Querier
	tx db.TxRunner
	// rand is where service tokens come from.
	rand io.Reader
}

func NewService(q sqlc.Querier, tx db.TxRunner) *Service {
	return &Service{q: q, tx: tx, rand: rand.Reader}
}

// WithRand makes service tokens come from r, so dev data is the same for the same seed.
func (s *Service) WithRand(r io.Reader) *Service {
	s.rand = r
	return s
}

// Identity is what the auth proxy's token says about the caller.
type Identity struct {
	Subject string
	Email   string
	Name    string
	Groups  []string
	// Expires is when the proof of this identity (the token) stops being accepted. Whoever
	// remembers the identity must forget it by then. Zero: it may not be remembered at all.
	Expires time.Time
}

// SignIn creates or updates the user for a verified identity and syncs their SSO groups.
func (s *Service) SignIn(ctx context.Context, id Identity) (Principal, error) {
	if strings.TrimSpace(id.Subject) == "" {
		return Principal{}, domain.ErrUnauthenticated
	}
	var p Principal
	err := s.tx.InTx(ctx, func(q sqlc.Querier) error {
		email := domain.Blank(&id.Email)
		if email != nil {
			// email is unique; a token cannot take over the address of another account.
			other, err := q.GetUserByEmail(ctx, *email)
			if err == nil && other.ExternalID != id.Subject {
				email = nil
			} else if err != nil && !errors.Is(domain.FromPG(err), domain.ErrNotFound) {
				return err
			}
		}
		u, err := q.UpsertUser(ctx, sqlc.UpsertUserParams{ExternalID: id.Subject, Email: email, Name: domain.Blank(&id.Name)})
		if err != nil {
			return err
		}
		// A subject that happens to equal a service account's external id must not become it.
		if !u.Active || u.Kind == sqlc.UserKindService {
			return domain.ErrForbidden
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
	return p, err
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

func (s *Service) Users(ctx context.Context, limit, offset int) ([]sqlc.AppUser, error) {
	if err := RequireAdmin(ctx); err != nil {
		return nil, err
	}
	l, o, err := domain.Page(limit, offset)
	if err != nil {
		return nil, err
	}
	return s.q.ListUsers(ctx, sqlc.ListUsersParams{Limit: l, Offset: o})
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
