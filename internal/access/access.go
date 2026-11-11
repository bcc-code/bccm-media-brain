// Package access decides who may do what: principals, permission levels, grants and the
// derived asset_access index. Domain services call it; resolvers do not.
package access

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
)

// Principal is the authenticated caller.
type Principal struct {
	UserID   string
	GroupIDs []string
	Admin    bool
}

// System is the principal of the CLI and dev-data scenarios. It has no user id.
func System() Principal { return Principal{Admin: true} }

// IDs are the ids grants can be given to: the user and their groups.
func (p Principal) IDs() []string {
	ids := make([]string, 0, 1+len(p.GroupIDs))
	if p.UserID != "" {
		ids = append(ids, p.UserID)
	}
	return append(ids, p.GroupIDs...)
}

// UserRef is the user id for created_by style columns, nil for the system principal.
func (p Principal) UserRef() *string {
	if p.UserID == "" {
		return nil
	}
	return &p.UserID
}

type ctxKey struct{}

func With(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

func From(ctx context.Context) (Principal, error) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	if !ok {
		return Principal{}, domain.ErrUnauthenticated
	}
	return p, nil
}

const (
	View     = sqlc.PermissionLevelView
	Download = sqlc.PermissionLevelDownloadOriginal
	Edit     = sqlc.PermissionLevelEdit
	Delete   = sqlc.PermissionLevelDelete
	Admin    = sqlc.PermissionLevelAdmin
	// None is the level of a principal without any grant.
	None = sqlc.PermissionLevel("")
)

var rank = map[sqlc.PermissionLevel]int{View: 1, Download: 2, Edit: 3, Delete: 4, Admin: 5}

func Allows(have, need sqlc.PermissionLevel) bool { return rank[have] >= rank[need] }

func higher(a, b sqlc.PermissionLevel) sqlc.PermissionLevel {
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// Decide hides objects from principals without any access and refuses those with too little.
func Decide(have, need sqlc.PermissionLevel) error {
	switch {
	case have == None:
		return domain.ErrNotFound
	case !Allows(have, need):
		return domain.ErrForbidden
	}
	return nil
}

func RequireAdmin(ctx context.Context) error {
	p, err := From(ctx)
	if err != nil {
		return err
	}
	if !p.Admin {
		return domain.ErrForbidden
	}
	return nil
}

func scopeLevel(ctx context.Context, q sqlc.Querier, p Principal, scope sqlc.GrantScope, id string) (sqlc.PermissionLevel, error) {
	l, err := q.ScopeLevel(ctx, sqlc.ScopeLevelParams{Scope: scope, ScopeID: id, Principals: p.IDs()})
	if errors.Is(err, pgx.ErrNoRows) {
		return None, nil
	}
	return l, err
}

// AssetLevels returns the caller's level per asset; assets without access are absent.
func AssetLevels(ctx context.Context, q sqlc.Querier, assetIDs []string) (map[string]sqlc.PermissionLevel, error) {
	p, err := From(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]sqlc.PermissionLevel, len(assetIDs))
	if p.Admin {
		for _, id := range assetIDs {
			out[id] = Admin
		}
		return out, nil
	}
	rows, err := q.AssetLevels(ctx, sqlc.AssetLevelsParams{AssetIds: assetIDs, Principals: p.IDs()})
	for _, r := range rows {
		out[r.AssetID] = r.Level
	}
	return out, err
}

func AssetLevel(ctx context.Context, q sqlc.Querier, assetID string) (sqlc.PermissionLevel, error) {
	levels, err := AssetLevels(ctx, q, []string{assetID})
	return levels[assetID], err
}

func RequireAsset(ctx context.Context, q sqlc.Querier, assetID string, need sqlc.PermissionLevel) error {
	have, err := AssetLevel(ctx, q, assetID)
	if err != nil {
		return err
	}
	return Decide(have, need)
}

// FileLevels returns, per file, the highest level the caller has on any asset that uses it.
func FileLevels(ctx context.Context, q sqlc.Querier, fileIDs []string) (map[string]sqlc.PermissionLevel, error) {
	p, err := From(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]sqlc.PermissionLevel, len(fileIDs))
	if p.Admin {
		for _, id := range fileIDs {
			out[id] = Admin
		}
		return out, nil
	}
	rows, err := q.FileLevels(ctx, sqlc.FileLevelsParams{FileIds: fileIDs, Principals: p.IDs()})
	for _, r := range rows {
		out[r.FileID] = r.Level
	}
	return out, err
}

func FileLevel(ctx context.Context, q sqlc.Querier, fileID string) (sqlc.PermissionLevel, error) {
	levels, err := FileLevels(ctx, q, []string{fileID})
	return levels[fileID], err
}

// RequireFileReuse checks that the caller may attach the file to another rendition. A file's
// level is the highest over every asset that uses it, so attaching widens who can see where it
// is stored: the caller must already have that right themselves (Download), not merely see the
// file. A file the caller cannot see is reported as invalid input, like any unknown reference.
func RequireFileReuse(ctx context.Context, q sqlc.Querier, fileID string) error {
	have, err := FileLevel(ctx, q, fileID)
	if err != nil {
		return err
	}
	if have == None {
		return domain.Invalid("file does not exist")
	}
	return Decide(have, Download)
}

func ProgramLevel(ctx context.Context, q sqlc.Querier, programID string) (sqlc.PermissionLevel, error) {
	p, err := From(ctx)
	if err != nil {
		return None, err
	}
	if p.Admin {
		return Admin, nil
	}
	have, err := scopeLevel(ctx, q, p, sqlc.GrantScopeProgram, programID)
	if err != nil || have != None {
		return have, err
	}
	// Seeing a project makes its program visible, so the user can navigate to it.
	seen, err := q.ProgramSeenViaProject(ctx, sqlc.ProgramSeenViaProjectParams{ProgramID: programID, Principals: p.IDs()})
	if err != nil || !seen {
		return None, err
	}
	return View, nil
}

// ProjectLevel combines grants on the project with grants on its program.
func ProjectLevel(ctx context.Context, q sqlc.Querier, project sqlc.Project) (sqlc.PermissionLevel, error) {
	p, err := From(ctx)
	if err != nil {
		return None, err
	}
	if p.Admin {
		return Admin, nil
	}
	own, err := scopeLevel(ctx, q, p, sqlc.GrantScopeProject, project.ID)
	if err != nil {
		return None, err
	}
	inherited, err := scopeLevel(ctx, q, p, sqlc.GrantScopeProgram, project.ProgramID)
	if err != nil {
		return None, err
	}
	return higher(own, inherited), nil
}

func RequireProject(ctx context.Context, q sqlc.Querier, project sqlc.Project, need sqlc.PermissionLevel) error {
	have, err := ProjectLevel(ctx, q, project)
	if err != nil {
		return err
	}
	return Decide(have, need)
}

// Refresh rebuilds asset_access for the given assets from the current grants and project links.
func Refresh(ctx context.Context, q sqlc.Querier, assetIDs []string) error {
	if len(assetIDs) == 0 {
		return nil
	}
	if err := q.DeleteAssetAccess(ctx, assetIDs); err != nil {
		return err
	}
	return q.InsertAssetAccess(ctx, assetIDs)
}

// RefreshScope refreshes every asset a grant on the given object reaches.
func RefreshScope(ctx context.Context, q sqlc.Querier, scope sqlc.GrantScope, id string) error {
	ids, err := q.AssetIDsInScope(ctx, sqlc.AssetIDsInScopeParams{Scope: scope, ScopeID: id})
	if err != nil {
		return err
	}
	return Refresh(ctx, q, ids)
}
