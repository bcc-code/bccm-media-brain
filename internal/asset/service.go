// Package asset manages assets and what hangs under them: versions, renditions, the files of a
// rendition and project links. It also serves the asset type and language reference lists.
package asset

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
)

type Service struct {
	q  sqlc.Querier
	tx db.TxRunner
}

func NewService(q sqlc.Querier, tx db.TxRunner) *Service { return &Service{q: q, tx: tx} }

type Input struct {
	TypeID           int16
	Title            string
	Description      *string
	CapturedAt       *time.Time
	Camera           *string
	Reel             *string
	Metadata         json.RawMessage
	LegacyIDs        []string
	OriginalLanguage *string
}

func (in *Input) validate() error {
	var err error
	if in.Title, err = domain.Required("title", in.Title); err != nil {
		return err
	}
	if in.Metadata, err = domain.JSONObject("metadata", in.Metadata); err != nil {
		return err
	}
	if in.LegacyIDs == nil {
		in.LegacyIDs = []string{}
	}
	return nil
}

// Create adds an asset. Everyone but administrators must create it inside a project they can
// edit, so that a new asset always has someone with access to it.
func (s *Service) Create(ctx context.Context, in Input, projectID *string) (sqlc.Asset, error) {
	p, err := access.From(ctx)
	if err != nil {
		return sqlc.Asset{}, err
	}
	if projectID == nil && !p.Admin {
		return sqlc.Asset{}, domain.InvalidField("projectId", "projectId is required")
	}
	if err := in.validate(); err != nil {
		return sqlc.Asset{}, err
	}
	var a sqlc.Asset
	err = s.tx.InTx(ctx, func(q sqlc.Querier) error {
		if projectID != nil {
			if err := requireProject(ctx, q, *projectID); err != nil {
				return err
			}
		}
		var err error
		a, err = q.CreateAsset(ctx, sqlc.CreateAssetParams{
			TypeID: in.TypeID, Title: in.Title, Description: domain.Blank(in.Description),
			CapturedAt: in.CapturedAt, Camera: domain.Blank(in.Camera), Reel: domain.Blank(in.Reel),
			Metadata: in.Metadata, LegacyIds: in.LegacyIDs, OriginalLanguage: domain.Blank(in.OriginalLanguage),
		})
		if err != nil {
			return domain.FromPG(err)
		}
		if projectID == nil {
			return nil
		}
		_, err = q.LinkAssetProject(ctx, sqlc.LinkAssetProjectParams{
			AssetID: a.ID, ProjectID: *projectID, Role: sqlc.LinkRoleSource, LinkOrigin: sqlc.LinkOriginManual,
		})
		if err != nil {
			return domain.FromPG(err)
		}
		return access.Refresh(ctx, q, []string{a.ID})
	})
	return a, access.Audited(ctx, err, "asset.create", a.ID, projectAttr(projectID)...)
}

func projectAttr(projectID *string) []any {
	if projectID == nil {
		return nil
	}
	return []any{"project", *projectID}
}

func requireProject(ctx context.Context, q sqlc.Querier, projectID string) error {
	pj, err := q.GetProject(ctx, projectID)
	if err != nil {
		return domain.FromPG(err)
	}
	return access.RequireProject(ctx, q, pj, access.Edit)
}

func (s *Service) asset(ctx context.Context, q sqlc.Querier, id string, need sqlc.PermissionLevel) (sqlc.Asset, error) {
	a, err := q.GetAsset(ctx, id)
	if err != nil {
		return sqlc.Asset{}, domain.FromPG(err)
	}
	return a, access.RequireAsset(ctx, q, id, need)
}

func (s *Service) Get(ctx context.Context, id string) (sqlc.Asset, error) {
	return s.asset(ctx, s.q, id, access.View)
}

type Filter struct {
	TypeID    *int16
	ProjectID *string
	Title     *string
	LegacyID  *string
}

func (s *Service) List(ctx context.Context, f Filter, limit, offset int) ([]sqlc.Asset, error) {
	p, err := access.From(ctx)
	if err != nil {
		return nil, err
	}
	l, o, err := domain.Page(limit, offset)
	if err != nil {
		return nil, err
	}
	return s.q.ListAssets(ctx, sqlc.ListAssetsParams{
		TypeID: f.TypeID, ProjectID: f.ProjectID, Title: domain.Blank(f.Title), LegacyID: f.LegacyID,
		IsAdmin: p.Admin, Principals: p.IDs(), Limit: l, Offset: o,
	})
}

// Update replaces every editable field. With expected, it is refused as a conflict when the
// asset is no longer at that revision.
func (s *Service) Update(ctx context.Context, id string, in Input, expected *int) (sqlc.Asset, error) {
	if _, err := s.asset(ctx, s.q, id, access.Edit); err != nil {
		return sqlc.Asset{}, err
	}
	if err := in.validate(); err != nil {
		return sqlc.Asset{}, err
	}
	rev, err := domain.ExpectedRevision(expected)
	if err != nil {
		return sqlc.Asset{}, err
	}
	a, err := s.q.UpdateAsset(ctx, sqlc.UpdateAssetParams{
		ID: id, TypeID: in.TypeID, Title: in.Title, Description: domain.Blank(in.Description),
		CapturedAt: in.CapturedAt, Camera: domain.Blank(in.Camera), Reel: domain.Blank(in.Reel),
		Metadata: in.Metadata, LegacyIds: in.LegacyIDs, OriginalLanguage: domain.Blank(in.OriginalLanguage),
		ExpectedRevision: rev,
	})
	err = domain.FromPGUpdate(err, rev, func() (int32, error) {
		now, err := s.q.GetAsset(ctx, id)
		return now.Revision, err
	})
	return a, access.Audited(ctx, err, "asset.update", id)
}

// Delete removes the asset with its versions, renditions and subtitles. Files stay.
func (s *Service) Delete(ctx context.Context, id string) error {
	if _, err := s.asset(ctx, s.q, id, access.Delete); err != nil {
		return err
	}
	_, err := s.q.DeleteAsset(ctx, id)
	return access.Audited(ctx, domain.FromPGDelete(err), "asset.delete", id)
}

func (s *Service) Link(ctx context.Context, assetID, projectID string, role sqlc.LinkRole) (sqlc.AssetProject, error) {
	if !role.Valid() {
		return sqlc.AssetProject{}, domain.InvalidField("role", "unknown role")
	}
	var link sqlc.AssetProject
	err := s.tx.InTx(ctx, func(q sqlc.Querier) error {
		if _, err := s.asset(ctx, q, assetID, access.Edit); err != nil {
			return err
		}
		if err := requireProject(ctx, q, projectID); err != nil {
			return err
		}
		var err error
		link, err = q.LinkAssetProject(ctx, sqlc.LinkAssetProjectParams{
			AssetID: assetID, ProjectID: projectID, Role: role, LinkOrigin: sqlc.LinkOriginManual,
		})
		if err != nil {
			return domain.FromPG(err)
		}
		return access.Refresh(ctx, q, []string{assetID})
	})
	return link, access.Audited(ctx, err, "asset.link", assetID, "project", projectID, "role", string(role))
}

func (s *Service) Unlink(ctx context.Context, assetID, projectID string, role sqlc.LinkRole) error {
	err := s.tx.InTx(ctx, func(q sqlc.Querier) error {
		if _, err := s.asset(ctx, q, assetID, access.Edit); err != nil {
			return err
		}
		n, err := q.UnlinkAssetProject(ctx, sqlc.UnlinkAssetProjectParams{AssetID: assetID, ProjectID: projectID, Role: role})
		if err != nil {
			return err
		}
		if n == 0 {
			return domain.ErrNotFound
		}
		return access.Refresh(ctx, q, []string{assetID})
	})
	return access.Audited(ctx, err, "asset.unlink", assetID, "project", projectID, "role", string(role))
}

type VersionInput struct {
	Label  *string
	Status *sqlc.VersionStatus
}

func (in VersionInput) status() (sqlc.VersionStatus, error) {
	if in.Status == nil {
		return sqlc.VersionStatusDraft, nil
	}
	if !in.Status.Valid() {
		return "", domain.InvalidField("status", "unknown status")
	}
	return *in.Status, nil
}

func (s *Service) CreateVersion(ctx context.Context, assetID string, in VersionInput) (sqlc.AssetVersion, error) {
	p, err := access.From(ctx)
	if err != nil {
		return sqlc.AssetVersion{}, err
	}
	if _, err := s.asset(ctx, s.q, assetID, access.Edit); err != nil {
		return sqlc.AssetVersion{}, err
	}
	status, err := in.status()
	if err != nil {
		return sqlc.AssetVersion{}, err
	}
	v, err := s.q.CreateAssetVersion(ctx, sqlc.CreateAssetVersionParams{
		AssetID: assetID, Label: domain.Blank(in.Label), Status: status, CreatedBy: p.UserRef(),
	})
	return v, access.Audited(ctx, domain.FromPG(err), "version.create", v.ID, "asset", assetID)
}

func (s *Service) version(ctx context.Context, id string, need sqlc.PermissionLevel) (sqlc.AssetVersion, error) {
	v, err := s.q.GetAssetVersion(ctx, id)
	if err != nil {
		return sqlc.AssetVersion{}, domain.FromPG(err)
	}
	return v, access.RequireAsset(ctx, s.q, v.AssetID, need)
}

func (s *Service) Version(ctx context.Context, id string) (sqlc.AssetVersion, error) {
	return s.version(ctx, id, access.View)
}

func (s *Service) UpdateVersion(ctx context.Context, id string, in VersionInput, expected *int) (sqlc.AssetVersion, error) {
	if _, err := s.version(ctx, id, access.Edit); err != nil {
		return sqlc.AssetVersion{}, err
	}
	status, err := in.status()
	if err != nil {
		return sqlc.AssetVersion{}, err
	}
	rev, err := domain.ExpectedRevision(expected)
	if err != nil {
		return sqlc.AssetVersion{}, err
	}
	v, err := s.q.UpdateAssetVersion(ctx, sqlc.UpdateAssetVersionParams{
		ID: id, Label: domain.Blank(in.Label), Status: status, ExpectedRevision: rev,
	})
	err = domain.FromPGUpdate(err, rev, func() (int32, error) {
		now, err := s.q.GetAssetVersion(ctx, id)
		return now.Revision, err
	})
	return v, access.Audited(ctx, err, "version.update", id)
}

func (s *Service) DeleteVersion(ctx context.Context, id string) error {
	if _, err := s.version(ctx, id, access.Delete); err != nil {
		return err
	}
	_, err := s.q.DeleteAssetVersion(ctx, id)
	return access.Audited(ctx, domain.FromPGDelete(err), "version.delete", id)
}

type RenditionInput struct {
	Kind      sqlc.RenditionKind // ignored on update
	Language  *string
	Purpose   *string
	Overrides json.RawMessage
	Metadata  json.RawMessage
}

func (in *RenditionInput) validate() error {
	var err error
	if in.Overrides, err = domain.JSONObject("overrides", in.Overrides); err != nil {
		return err
	}
	in.Metadata, err = domain.JSONObject("metadata", in.Metadata)
	return err
}

func (s *Service) CreateRendition(ctx context.Context, versionID string, in RenditionInput) (sqlc.Rendition, error) {
	if _, err := s.version(ctx, versionID, access.Edit); err != nil {
		return sqlc.Rendition{}, err
	}
	if !in.Kind.Valid() {
		return sqlc.Rendition{}, domain.InvalidField("kind", "unknown kind")
	}
	if err := in.validate(); err != nil {
		return sqlc.Rendition{}, err
	}
	r, err := s.q.CreateRendition(ctx, sqlc.CreateRenditionParams{
		VersionID: versionID, Kind: in.Kind, Language: domain.Blank(in.Language),
		Purpose: domain.Blank(in.Purpose), Overrides: in.Overrides, Metadata: in.Metadata,
	})
	return r, access.Audited(ctx, domain.FromPG(err), "rendition.create", r.ID, "version", versionID)
}

func (s *Service) rendition(ctx context.Context, id string, need sqlc.PermissionLevel) (sqlc.Rendition, error) {
	r, err := s.q.GetRendition(ctx, id)
	if err != nil {
		return sqlc.Rendition{}, domain.FromPG(err)
	}
	return r.Rendition, access.RequireAsset(ctx, s.q, r.AssetID, need)
}

func (s *Service) Rendition(ctx context.Context, id string) (sqlc.Rendition, error) {
	return s.rendition(ctx, id, access.View)
}

func (s *Service) UpdateRendition(ctx context.Context, id string, in RenditionInput, expected *int) (sqlc.Rendition, error) {
	if _, err := s.rendition(ctx, id, access.Edit); err != nil {
		return sqlc.Rendition{}, err
	}
	if err := in.validate(); err != nil {
		return sqlc.Rendition{}, err
	}
	rev, err := domain.ExpectedRevision(expected)
	if err != nil {
		return sqlc.Rendition{}, err
	}
	r, err := s.q.UpdateRendition(ctx, sqlc.UpdateRenditionParams{
		ID: id, Language: domain.Blank(in.Language), Purpose: domain.Blank(in.Purpose),
		Overrides: in.Overrides, Metadata: in.Metadata, ExpectedRevision: rev,
	})
	err = domain.FromPGUpdate(err, rev, func() (int32, error) {
		now, err := s.q.GetRendition(ctx, id)
		return now.Rendition.Revision, err
	})
	return r, access.Audited(ctx, err, "rendition.update", id)
}

func (s *Service) DeleteRendition(ctx context.Context, id string) error {
	if _, err := s.rendition(ctx, id, access.Delete); err != nil {
		return err
	}
	_, err := s.q.DeleteRendition(ctx, id)
	return access.Audited(ctx, domain.FromPGDelete(err), "rendition.delete", id)
}

// AttachFile adds a file to a rendition. Besides Edit on the rendition, the caller needs
// Download on the file: see access.RequireFileReuse.
func (s *Service) AttachFile(ctx context.Context, renditionID, fileID string, role sqlc.FileRole, position int32) (sqlc.RenditionFile, error) {
	if _, err := s.rendition(ctx, renditionID, access.Edit); err != nil {
		return sqlc.RenditionFile{}, err
	}
	if !role.Valid() {
		return sqlc.RenditionFile{}, domain.InvalidField("role", "unknown role")
	}
	if err := access.RequireFileReuse(ctx, s.q, fileID); err != nil {
		return sqlc.RenditionFile{}, err
	}
	rf, err := s.q.AttachFile(ctx, sqlc.AttachFileParams{RenditionID: renditionID, FileID: fileID, Role: role, Position: position})
	return rf, access.Audited(ctx, domain.FromPG(err), "rendition.attach_file", renditionID, "file", fileID, "role", string(role))
}

func (s *Service) DetachFile(ctx context.Context, renditionID, fileID string, role sqlc.FileRole) error {
	if _, err := s.rendition(ctx, renditionID, access.Edit); err != nil {
		return err
	}
	n, err := s.q.DetachFile(ctx, sqlc.DetachFileParams{RenditionID: renditionID, FileID: fileID, Role: role})
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return access.Audited(ctx, nil, "rendition.detach_file", renditionID, "file", fileID, "role", string(role))
}

func (s *Service) Types(ctx context.Context) ([]sqlc.AssetType, error) {
	return s.q.ListAssetTypes(ctx)
}

func (s *Service) Languages(ctx context.Context, activeOnly bool) ([]sqlc.Language, error) {
	return s.q.ListLanguages(ctx, activeOnly)
}

// The ...For methods below load children for assets, versions and renditions the caller was
// already allowed to see; they are the batch side of the GraphQL dataloaders.

func (s *Service) LevelsFor(ctx context.Context, assetIDs []string) (map[string]sqlc.PermissionLevel, error) {
	return access.AssetLevels(ctx, s.q, assetIDs)
}

// VersionsFor returns one page of versions per asset, by number.
func (s *Service) VersionsFor(ctx context.Context, assetIDs []string, limit, offset int) (map[string][]sqlc.AssetVersion, error) {
	l, o, err := domain.Page(limit, offset)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListAssetVersions(ctx, sqlc.ListAssetVersionsParams{AssetIds: assetIDs, Limit: l, Offset: o})
	out := map[string][]sqlc.AssetVersion{}
	for _, r := range rows {
		out[r.AssetID] = append(out[r.AssetID], r)
	}
	return out, err
}

// LinksFor leaves out links to projects the caller may not see: the link itself (project id,
// role, when it was made) would reveal a project that an asset grant does not give access to.
func (s *Service) LinksFor(ctx context.Context, assetIDs []string) (map[string][]sqlc.AssetProject, error) {
	p, err := access.From(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListAssetProjects(ctx, assetIDs)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	projectIDs := make([]string, 0, len(rows))
	for _, r := range rows {
		projectIDs = append(projectIDs, r.ProjectID)
	}
	visible, err := s.q.ListVisibleProjects(ctx, sqlc.ListVisibleProjectsParams{
		Ids: projectIDs, ProgramIds: []string{}, IsAdmin: p.Admin, Principals: p.IDs(), PerProgram: int32(len(projectIDs)),
	})
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(visible))
	for _, pj := range visible {
		seen[pj.ID] = true
	}
	out := map[string][]sqlc.AssetProject{}
	for _, r := range rows {
		if seen[r.ProjectID] {
			out[r.AssetID] = append(out[r.AssetID], r)
		}
	}
	return out, nil
}

func (s *Service) RenditionsFor(ctx context.Context, versionIDs []string) (map[string][]sqlc.Rendition, error) {
	rows, err := s.q.ListRenditions(ctx, versionIDs)
	out := map[string][]sqlc.Rendition{}
	for _, r := range rows {
		out[r.VersionID] = append(out[r.VersionID], r)
	}
	return out, err
}

func (s *Service) FilesFor(ctx context.Context, renditionIDs []string) (map[string][]sqlc.RenditionFile, error) {
	rows, err := s.q.ListRenditionFiles(ctx, renditionIDs)
	out := map[string][]sqlc.RenditionFile{}
	for _, r := range rows {
		out[r.RenditionID] = append(out[r.RenditionID], r)
	}
	return out, err
}
