package access

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
)

// serviceSubject prefixes the external id of service accounts.
const serviceSubject = "service|"

type ServiceAccountInput struct {
	Name  string
	Admin bool
}

// CreateServiceAccount adds a machine client. It can do nothing until it has a token and,
// unless it is an administrator, grants.
func (s *Service) CreateServiceAccount(ctx context.Context, in ServiceAccountInput) (sqlc.AppUser, error) {
	if err := RequirePersonAdmin(ctx); err != nil {
		return sqlc.AppUser{}, err
	}
	name, err := domain.Required("name", in.Name)
	if err != nil {
		return sqlc.AppUser{}, err
	}
	// A name that looks like an id would be read as one wherever an id or name is accepted.
	if strings.HasPrefix(name, "US-") {
		return sqlc.AppUser{}, domain.Invalid("name must not start with US-")
	}
	u, err := s.q.CreateServiceAccount(ctx, sqlc.CreateServiceAccountParams{
		ExternalID: serviceSubject + name, Name: name, IsAdmin: in.Admin,
	})
	return u, domain.FromPG(err)
}

func (s *Service) ServiceAccounts(ctx context.Context) ([]sqlc.AppUser, error) {
	if err := RequirePersonAdmin(ctx); err != nil {
		return nil, err
	}
	return s.q.ListServiceAccounts(ctx)
}

// serviceAccount finds a service account by id or name. Other users are not found.
func (s *Service) serviceAccount(ctx context.Context, ref string) (sqlc.AppUser, error) {
	var u sqlc.AppUser
	var err error
	if strings.HasPrefix(ref, "US-") {
		u, err = s.q.GetUser(ctx, ref)
	} else {
		u, err = s.q.GetUserByExternalID(ctx, serviceSubject+ref)
	}
	if err != nil {
		return sqlc.AppUser{}, domain.FromPG(err)
	}
	if u.Kind != sqlc.UserKindService {
		return sqlc.AppUser{}, domain.ErrNotFound
	}
	return u, nil
}

// SetServiceAccountActive switches a service account (id or name) off or on. Switching it off
// refuses all its tokens without revoking them.
func (s *Service) SetServiceAccountActive(ctx context.Context, ref string, active bool) (sqlc.AppUser, error) {
	if err := RequirePersonAdmin(ctx); err != nil {
		return sqlc.AppUser{}, err
	}
	u, err := s.serviceAccount(ctx, ref)
	if err != nil {
		return sqlc.AppUser{}, err
	}
	u, err = s.q.SetServiceAccountActive(ctx, sqlc.SetServiceAccountActiveParams{ID: u.ID, Active: active})
	return u, domain.FromPG(err)
}

type ServiceTokenInput struct {
	// ServiceAccount is the account's id or name.
	ServiceAccount string
	Name           string
	// ExpiresAt defaults to a year from now.
	ExpiresAt *time.Time
}

// NewServiceToken carries the token itself, which exists nowhere else afterwards.
type NewServiceToken struct {
	Token        string
	ServiceToken sqlc.ServiceToken
}

func (s *Service) CreateServiceToken(ctx context.Context, in ServiceTokenInput) (NewServiceToken, error) {
	if err := RequirePersonAdmin(ctx); err != nil {
		return NewServiceToken{}, err
	}
	p, _ := From(ctx)
	name, err := domain.Required("name", in.Name)
	if err != nil {
		return NewServiceToken{}, err
	}
	expires := time.Now().Add(defaultTokenTTL)
	if in.ExpiresAt != nil {
		if expires = *in.ExpiresAt; !expires.After(time.Now()) {
			return NewServiceToken{}, domain.Invalid("expiresAt must be in the future")
		}
	}
	u, err := s.serviceAccount(ctx, in.ServiceAccount)
	if err != nil {
		return NewServiceToken{}, err
	}
	token, err := newToken(s.rand)
	if err != nil {
		return NewServiceToken{}, err
	}
	row, err := s.q.CreateServiceToken(ctx, sqlc.CreateServiceTokenParams{
		UserID: u.ID, Name: name, TokenHash: hashToken(token), CreatedBy: p.UserRef(), ExpiresAt: expires,
	})
	if err != nil {
		return NewServiceToken{}, domain.FromPG(err)
	}
	return NewServiceToken{Token: token, ServiceToken: row}, nil
}

// RevokeServiceToken is final; revoking twice keeps the first time.
func (s *Service) RevokeServiceToken(ctx context.Context, id string) (sqlc.ServiceToken, error) {
	if err := RequirePersonAdmin(ctx); err != nil {
		return sqlc.ServiceToken{}, err
	}
	t, err := s.q.RevokeServiceToken(ctx, id)
	return t, domain.FromPG(err)
}

func (s *Service) ServiceTokens(ctx context.Context, account string) ([]sqlc.ServiceToken, error) {
	if err := RequirePersonAdmin(ctx); err != nil {
		return nil, err
	}
	u, err := s.serviceAccount(ctx, account)
	if err != nil {
		return nil, err
	}
	return s.q.ListServiceTokens(ctx, u.ID)
}

// ServiceSignIn turns a service token into its account's principal and says when the token
// expires. Unknown, revoked and expired tokens and tokens of inactive accounts are all refused
// the same way.
func (s *Service) ServiceSignIn(ctx context.Context, token string) (Principal, time.Time, error) {
	row, err := s.q.UseServiceToken(ctx, hashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, time.Time{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, time.Time{}, err
	}
	groups, err := s.q.ListUserGroupIDs(ctx, row.UserID)
	if err != nil {
		return Principal{}, time.Time{}, err
	}
	p := Principal{UserID: row.UserID, GroupIDs: groups, Admin: row.IsAdmin, Service: true, TokenID: row.TokenID}
	return p, row.ExpiresAt, nil
}
