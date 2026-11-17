package access

import (
	"context"
	"strings"

	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
)

// serviceSubject prefixes the external id of a service account until its first sign-in, when
// the subject of its token takes its place.
const serviceSubject = "service|"

// Pending reports whether a service account has not signed in yet.
func Pending(u sqlc.AppUser) bool {
	return u.Kind == sqlc.UserKindService && strings.HasPrefix(u.ExternalID, serviceSubject)
}

type ServiceAccountInput struct {
	// Name is the account's username at the identity provider: its first sign-in is matched
	// on it. Unique among service accounts.
	Name  string
	Admin bool
	// User is the id of an account that has already signed in as a person and is to become
	// this service account. Nil: a new account, which signs in later.
	User *string
}

// CreateServiceAccount adds a machine client, which signs in with a token of the identity
// provider that carries the service group. Unless it is an administrator it can do nothing
// until it has grants.
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
		return sqlc.AppUser{}, domain.InvalidField("name", "name must not start with US-")
	}
	var u sqlc.AppUser
	if in.User != nil {
		// No row also when the user is a service account already: there is nothing to make.
		u, err = s.q.MakeServiceAccount(ctx, sqlc.MakeServiceAccountParams{ID: *in.User, Name: name, IsAdmin: in.Admin})
	} else {
		u, err = s.q.CreateServiceAccount(ctx, sqlc.CreateServiceAccountParams{
			ExternalID: serviceSubject + name, Name: name, IsAdmin: in.Admin,
		})
	}
	return u, Audited(ctx, domain.FromPG(err), "service_account.create", u.ID, "name", name, "admin", in.Admin, "from_person", in.User != nil)
}

func (s *Service) ServiceAccounts(ctx context.Context) ([]sqlc.AppUser, error) {
	if err := RequirePersonAdmin(ctx); err != nil {
		return nil, err
	}
	return s.q.ListServiceAccounts(ctx)
}

// serviceAccount finds a service account by id or name. Other users are not found.
func (s *Service) serviceAccount(ctx context.Context, ref string) (sqlc.AppUser, error) {
	if !strings.HasPrefix(ref, "US-") {
		u, err := s.q.GetServiceAccountByName(ctx, ref)
		return u, domain.FromPG(err)
	}
	u, err := s.q.GetUser(ctx, ref)
	if err != nil {
		return sqlc.AppUser{}, domain.FromPG(err)
	}
	if u.Kind != sqlc.UserKindService {
		return sqlc.AppUser{}, domain.ErrNotFound
	}
	return u, nil
}

// SetServiceAccountActive switches a service account (id or name) off or on.
func (s *Service) SetServiceAccountActive(ctx context.Context, ref string, active bool) (sqlc.AppUser, error) {
	if err := RequirePersonAdmin(ctx); err != nil {
		return sqlc.AppUser{}, err
	}
	u, err := s.serviceAccount(ctx, ref)
	if err != nil {
		return sqlc.AppUser{}, err
	}
	u, err = s.q.SetServiceAccountActive(ctx, sqlc.SetServiceAccountActiveParams{ID: u.ID, Active: active})
	return u, Audited(ctx, domain.FromPG(err), "service_account.set_active", u.ID, "active", active)
}
