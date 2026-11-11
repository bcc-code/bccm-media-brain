// Package project manages programs and the projects under them.
package project

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
)

var (
	programCode = regexp.MustCompile(`^[A-Z]{4,5}$`)
	projectCode = regexp.MustCompile(`^[A-Z]{4,5}_[A-Za-z0-9_]+$`)
)

type Service struct {
	q  sqlc.Querier
	tx db.TxRunner
}

func NewService(q sqlc.Querier, tx db.TxRunner) *Service { return &Service{q: q, tx: tx} }

type ProgramInput struct {
	Code        string // ignored on update
	Name        string
	Description *string
	Metadata    json.RawMessage
}

func (s *Service) CreateProgram(ctx context.Context, in ProgramInput) (sqlc.Program, error) {
	if err := access.RequireAdmin(ctx); err != nil {
		return sqlc.Program{}, err
	}
	if !programCode.MatchString(in.Code) {
		return sqlc.Program{}, domain.Invalid("code must be 4-5 capital letters")
	}
	name, meta, err := nameAndMeta(in.Name, in.Metadata)
	if err != nil {
		return sqlc.Program{}, err
	}
	p, err := s.q.CreateProgram(ctx, sqlc.CreateProgramParams{
		Code: in.Code, Name: name, Description: domain.Blank(in.Description), Metadata: meta,
	})
	return p, access.Audited(ctx, domain.FromPG(err), "program.create", p.ID, "code", p.Code)
}

func nameAndMeta(name string, meta json.RawMessage) (string, json.RawMessage, error) {
	name, err := domain.Required("name", name)
	if err != nil {
		return "", nil, err
	}
	meta, err = domain.JSONObject("metadata", meta)
	return name, meta, err
}

func (s *Service) program(ctx context.Context, id string, need sqlc.PermissionLevel) (sqlc.Program, error) {
	p, err := s.q.GetProgram(ctx, id)
	if err != nil {
		return sqlc.Program{}, domain.FromPG(err)
	}
	have, err := access.ProgramLevel(ctx, s.q, id)
	if err != nil {
		return sqlc.Program{}, err
	}
	return p, access.Decide(have, need)
}

func (s *Service) Program(ctx context.Context, id string) (sqlc.Program, error) {
	return s.program(ctx, id, access.View)
}

func (s *Service) Programs(ctx context.Context, limit, offset int) ([]sqlc.Program, error) {
	p, err := access.From(ctx)
	if err != nil {
		return nil, err
	}
	l, o, err := domain.Page(limit, offset)
	if err != nil {
		return nil, err
	}
	return s.q.ListPrograms(ctx, sqlc.ListProgramsParams{IsAdmin: p.Admin, Principals: p.IDs(), Limit: l, Offset: o})
}

func (s *Service) UpdateProgram(ctx context.Context, id string, in ProgramInput, expected *int) (sqlc.Program, error) {
	if _, err := s.program(ctx, id, access.Edit); err != nil {
		return sqlc.Program{}, err
	}
	name, meta, err := nameAndMeta(in.Name, in.Metadata)
	if err != nil {
		return sqlc.Program{}, err
	}
	rev, err := domain.ExpectedRevision(expected)
	if err != nil {
		return sqlc.Program{}, err
	}
	p, err := s.q.UpdateProgram(ctx, sqlc.UpdateProgramParams{
		ID: id, Name: name, Description: domain.Blank(in.Description), Metadata: meta, ExpectedRevision: rev,
	})
	err = domain.FromPGUpdate(err, rev, func() (int32, error) {
		now, err := s.q.GetProgram(ctx, id)
		return now.Revision, err
	})
	return p, access.Audited(ctx, err, "program.update", id)
}

func (s *Service) DeleteProgram(ctx context.Context, id string) error {
	if err := access.RequireAdmin(ctx); err != nil {
		return err
	}
	n, err := s.q.DeleteProgram(ctx, id)
	if err != nil {
		return domain.FromPGDelete(err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return access.Audited(ctx, nil, "program.delete", id)
}

type ProjectInput struct {
	ProgramID string // ignored on update
	Code      string // ignored on update
	Name      string
	Status    *sqlc.ProjectStatus
	StartsOn  *time.Time
	EndsOn    *time.Time
	Metadata  json.RawMessage
}

func (in ProjectInput) status() (sqlc.ProjectStatus, error) {
	if in.Status == nil {
		return sqlc.ProjectStatusPlanned, nil
	}
	if !in.Status.Valid() {
		return "", domain.Invalid("unknown status")
	}
	return *in.Status, nil
}

func (in ProjectInput) checkDates() error {
	if in.StartsOn != nil && in.EndsOn != nil && in.EndsOn.Before(*in.StartsOn) {
		return domain.Invalid("endsOn must not be before startsOn")
	}
	return nil
}

func (s *Service) CreateProject(ctx context.Context, in ProjectInput) (sqlc.Project, error) {
	prog, err := s.program(ctx, in.ProgramID, access.Edit)
	if err != nil {
		return sqlc.Project{}, err
	}
	if !projectCode.MatchString(in.Code) || !strings.HasPrefix(in.Code, prog.Code+"_") {
		return sqlc.Project{}, domain.Invalid("code must be %s_ followed by letters, digits or underscores", prog.Code)
	}
	name, meta, err := nameAndMeta(in.Name, in.Metadata)
	if err != nil {
		return sqlc.Project{}, err
	}
	status, err := in.status()
	if err != nil {
		return sqlc.Project{}, err
	}
	if err := in.checkDates(); err != nil {
		return sqlc.Project{}, err
	}
	p, err := s.q.CreateProject(ctx, sqlc.CreateProjectParams{
		ProgramID: prog.ID, Code: in.Code, Name: name, Status: status,
		StartsOn: in.StartsOn, EndsOn: in.EndsOn, Metadata: meta,
	})
	return p, access.Audited(ctx, domain.FromPG(err), "project.create", p.ID, "program", p.ProgramID, "code", p.Code)
}

func (s *Service) project(ctx context.Context, q sqlc.Querier, id string, need sqlc.PermissionLevel) (sqlc.Project, error) {
	p, err := q.GetProject(ctx, id)
	if err != nil {
		return sqlc.Project{}, domain.FromPG(err)
	}
	return p, access.RequireProject(ctx, q, p, need)
}

func (s *Service) Project(ctx context.Context, id string) (sqlc.Project, error) {
	return s.project(ctx, s.q, id, access.View)
}

type ProjectFilter struct {
	ProgramID *string
	Status    *sqlc.ProjectStatus
}

func (s *Service) Projects(ctx context.Context, f ProjectFilter, limit, offset int) ([]sqlc.Project, error) {
	p, err := access.From(ctx)
	if err != nil {
		return nil, err
	}
	l, o, err := domain.Page(limit, offset)
	if err != nil {
		return nil, err
	}
	var status *string
	if f.Status != nil {
		st := string(*f.Status)
		status = &st
	}
	return s.q.ListProjects(ctx, sqlc.ListProjectsParams{
		ProgramID: f.ProgramID, Status: status, IsAdmin: p.Admin, Principals: p.IDs(), Limit: l, Offset: o,
	})
}

func (s *Service) UpdateProject(ctx context.Context, id string, in ProjectInput, expected *int) (sqlc.Project, error) {
	if _, err := s.project(ctx, s.q, id, access.Edit); err != nil {
		return sqlc.Project{}, err
	}
	name, meta, err := nameAndMeta(in.Name, in.Metadata)
	if err != nil {
		return sqlc.Project{}, err
	}
	status, err := in.status()
	if err != nil {
		return sqlc.Project{}, err
	}
	if err := in.checkDates(); err != nil {
		return sqlc.Project{}, err
	}
	rev, err := domain.ExpectedRevision(expected)
	if err != nil {
		return sqlc.Project{}, err
	}
	p, err := s.q.UpdateProject(ctx, sqlc.UpdateProjectParams{
		ID: id, Name: name, Status: status, StartsOn: in.StartsOn, EndsOn: in.EndsOn, Metadata: meta,
		ExpectedRevision: rev,
	})
	err = domain.FromPGUpdate(err, rev, func() (int32, error) {
		now, err := s.q.GetProject(ctx, id)
		return now.Revision, err
	})
	return p, access.Audited(ctx, err, "project.update", id)
}

// DeleteProject also drops the access its grants gave to the linked assets.
func (s *Service) DeleteProject(ctx context.Context, id string) error {
	err := s.tx.InTx(ctx, func(q sqlc.Querier) error {
		if _, err := s.project(ctx, q, id, access.Delete); err != nil {
			return err
		}
		assets, err := q.AssetIDsInScope(ctx, sqlc.AssetIDsInScopeParams{Scope: sqlc.GrantScopeProject, ScopeID: id})
		if err != nil {
			return err
		}
		if _, err := q.DeleteProject(ctx, id); err != nil {
			return domain.FromPGDelete(err)
		}
		return access.Refresh(ctx, q, assets)
	})
	return access.Audited(ctx, err, "project.delete", id)
}

// ProgramsByID loads the programs of projects the caller was already allowed to see.
func (s *Service) ProgramsByID(ctx context.Context, ids []string) (map[string]sqlc.Program, error) {
	rows, err := s.q.GetPrograms(ctx, ids)
	out := make(map[string]sqlc.Program, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, err
}

func (s *Service) visibleProjects(ctx context.Context, ids, programIDs []string) ([]sqlc.Project, error) {
	p, err := access.From(ctx)
	if err != nil {
		return nil, err
	}
	return s.q.ListVisibleProjects(ctx, sqlc.ListVisibleProjectsParams{
		Ids: ids, ProgramIds: programIDs, IsAdmin: p.Admin, Principals: p.IDs(),
	})
}

// ProjectsByID returns the projects the caller may see; the others are absent.
func (s *Service) ProjectsByID(ctx context.Context, ids []string) (map[string]sqlc.Project, error) {
	rows, err := s.visibleProjects(ctx, ids, []string{})
	out := make(map[string]sqlc.Project, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, err
}

// ProjectsFor returns, per program, the projects the caller may see.
func (s *Service) ProjectsFor(ctx context.Context, programIDs []string) (map[string][]sqlc.Project, error) {
	rows, err := s.visibleProjects(ctx, []string{}, programIDs)
	out := map[string][]sqlc.Project{}
	for _, r := range rows {
		out[r.ProgramID] = append(out[r.ProgramID], r)
	}
	return out, err
}
