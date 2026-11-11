package devdata

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/asset"
	"github.com/bcc-media/mediabrain/internal/cantemo"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/fake"
	"github.com/bcc-media/mediabrain/internal/file"
	"github.com/bcc-media/mediabrain/internal/project"
	"github.com/bcc-media/mediabrain/internal/subtitle"
	"github.com/bcc-media/mediabrain/pkg/vidispine"
)

// registry lists every scenario shown in the debug UI and the seed CLI. Scenarios go through
// the domain services, so the data they create respects the same rules as the API.
var registry = []Scenario{
	{
		Name:        "empty",
		Description: "Wipe all data (keeps schema, migration history and reference tables).",
		AlwaysReset: true,
		Run: func(context.Context, Env, Values) (string, error) {
			return "all tables truncated", nil
		},
	},
	{
		Name:        "catalog",
		Description: "Programs with projects, assets (versions, renditions, files, copies, subtitles), users and grants.",
		Params: []Param{
			{Name: "programs", Label: "Programs", Type: Int, Default: "2", Min: ptr(1), Max: ptr(50)},
			{Name: "projects", Label: "Projects per program", Type: Int, Default: "2", Min: ptr(1), Max: ptr(50)},
			{Name: "assets", Label: "Assets per project", Type: Int, Default: "5", Min: ptr(1), Max: ptr(500)},
			{Name: "users", Label: "Users", Type: Int, Default: "3", Min: ptr(0), Max: ptr(100), Help: "Each gets a random grant"},
			{Name: "subtitles", Label: "Subtitles", Type: Bool, Default: "true"},
		},
		Run: runCatalog,
	},
	{
		Name: "access",
		Description: "One program with two projects and a persona per permission case: admin@, editor@ (edit on the program), " +
			"viewer@ (view on project 1), downloader@ (download on one asset), member@ (via a group, view on project 2), " +
			"expired@ (grant ran out), inactive@ and nobody@ — all @example.test. Pick one under Users to act as them.",
		AlwaysReset: true,
		Run:         runAccess,
	},
	{
		Name:        "subtitle-revisions",
		Description: "One asset whose subtitles were imported and then edited several times.",
		Params: []Param{
			{Name: "edits", Label: "Edits", Type: Int, Default: "3", Min: ptr(0), Max: ptr(50)},
			{Name: "cues", Label: "Cues", Type: Int, Default: "40", Min: ptr(1), Max: ptr(5000)},
		},
		Run: runSubtitleRevisions,
	},
	{
		Name: "cantemo",
		Description: "Assets as an import from Cantemo leaves them: the item id in legacyIds, Cantemo's fields under " +
			"metadata.cantemo, and the original file on a storage location named after the Vidispine storage. One more " +
			"item is a placeholder without files and without a project. No Cantemo is contacted: the items are made up.",
		Params: []Param{
			{Name: "items", Label: "Items", Type: Int, Default: "5", Min: ptr(1), Max: ptr(500)},
		},
		Run: runCantemo,
	},
	{
		Name: "edge-cases",
		Description: "Awkward data: unicode, very long and markup-looking titles, a zero-byte file, missing and mismatched " +
			"copies, one file in several locations, an asset without versions, a version without renditions, " +
			"overlapping and very long cues, zxx and und languages.",
		Run: runEdgeCases,
	},
}

type services struct {
	access    *access.Service
	projects  *project.Service
	files     *file.Service
	assets    *asset.Service
	subtitles *subtitle.Service
	// cantemo imports from the made-up items added to source.
	cantemo *cantemo.Service
	source  *cantemoItems
}

func newServices(q sqlc.Querier) services {
	tx := db.SameTx(q)
	source := &cantemoItems{items: map[string]fake.CantemoItem{}}
	return services{
		access: access.NewService(q, tx), projects: project.NewService(q, tx), files: file.NewService(q, tx),
		assets: asset.NewService(q, tx), subtitles: subtitle.NewService(q, tx),
		cantemo: cantemo.NewService(q, tx, source), source: source,
	}
}

// cantemoItems stands in for the Vidispine API with items held in memory.
type cantemoItems struct{ items map[string]fake.CantemoItem }

func (c *cantemoItems) GetMetadata(_ context.Context, itemID string) (*vidispine.MetadataResult, error) {
	out := &vidispine.MetadataResult{ID: itemID, Terse: map[string][]*vidispine.MetadataField{}}
	for name, value := range c.items[itemID].Fields {
		out.Terse[name] = []*vidispine.MetadataField{{Start: vidispine.MinusInf, End: vidispine.PlusInf, Value: value}}
	}
	return out, nil
}

func (c *cantemoItems) GetShapes(_ context.Context, itemID string) (*vidispine.ShapeResult, error) {
	return &vidispine.ShapeResult{ID: itemID, Shape: c.items[itemID].Shapes}, nil
}

func (c *cantemoItems) GetStorageMethods(_ context.Context, storageID string) (*vidispine.StorageResult, error) {
	return &vidispine.StorageResult{Methods: []vidispine.StorageMethod{{URI: "file:///mnt/" + storageID + "/"}}}, nil
}

func runCantemo(ctx context.Context, env Env, v Values) (string, error) {
	s, f := newServices(env.Q), env.Fake
	prog, err := s.program(ctx, f)
	if err != nil {
		return "", err
	}
	pj, err := s.project(ctx, f, prog)
	if err != nil {
		return "", err
	}
	storage := fmt.Sprintf("VX-%d", f.Int(1, 99))
	for range v.Int("items") {
		item := f.CantemoItem(storage)
		s.source.items[item.ID] = item
		if _, err := s.cantemo.Import(ctx, cantemo.ImportInput{ItemID: item.ID, TypeID: 1, ProjectID: &pj.ID}); err != nil {
			return "", err
		}
	}
	placeholder := f.CantemoPlaceholder()
	s.source.items[placeholder.ID] = placeholder
	a, err := s.cantemo.Import(ctx, cantemo.ImportInput{ItemID: placeholder.ID, TypeID: 1})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d items imported into project %s from storage %s, placeholder %s as %s",
		v.Int("items"), pj.Code, storage, placeholder.ID, a.ID), nil
}

func (s services) program(ctx context.Context, f *fake.Faker) (sqlc.Program, error) {
	return s.projects.CreateProgram(ctx, f.Program())
}

func (s services) project(ctx context.Context, f *fake.Faker, prog sqlc.Program) (sqlc.Project, error) {
	return s.projects.CreateProject(ctx, f.Project(prog.ID, prog.Code))
}

// filmedAsset creates an asset in the project with one version, a master rendition and a
// video file that has a copy at each of the given locations.
func (s services) filmedAsset(ctx context.Context, f *fake.Faker, projectID string, in asset.Input, locations ...sqlc.StorageLocation) (sqlc.Asset, sqlc.AssetVersion, error) {
	a, err := s.assets.Create(ctx, in, &projectID)
	if err != nil {
		return a, sqlc.AssetVersion{}, err
	}
	label := "Camera original"
	v, err := s.assets.CreateVersion(ctx, a.ID, asset.VersionInput{Label: &label})
	if err != nil {
		return a, v, err
	}
	r, err := s.assets.CreateRendition(ctx, v.ID, asset.RenditionInput{Kind: sqlc.RenditionKindMaster, Language: in.OriginalLanguage})
	if err != nil {
		return a, v, err
	}
	video := f.VideoFile()
	for _, l := range locations {
		video.Copies = append(video.Copies, file.CopyInput{LocationID: l.ID, Path: f.Path("mov")})
	}
	fl, err := s.files.Register(ctx, video)
	if err != nil {
		return a, v, err
	}
	_, err = s.assets.AttachFile(ctx, r.ID, fl.ID, sqlc.FileRoleVideo, 0)
	return a, v, err
}

func (s services) importSRT(ctx context.Context, versionID, language, content string) (sqlc.SubtitleTrack, error) {
	return s.subtitles.Import(ctx, subtitle.ImportInput{VersionID: versionID, Language: &language, Format: subtitle.SRT, Content: content})
}

func (s services) locations(ctx context.Context, f *fake.Faker, n int) ([]sqlc.StorageLocation, error) {
	out := make([]sqlc.StorageLocation, n)
	for i := range out {
		l, err := s.files.CreateLocation(ctx, f.Location())
		if err != nil {
			return nil, err
		}
		out[i] = l
	}
	return out, nil
}

func runCatalog(ctx context.Context, env Env, v Values) (string, error) {
	s, f := newServices(env.Q), env.Fake
	locations, err := s.locations(ctx, f, 2)
	if err != nil {
		return "", err
	}
	var programs []sqlc.Program
	var projects []sqlc.Project
	assets := 0
	for range v.Int("programs") {
		prog, err := s.program(ctx, f)
		if err != nil {
			return "", err
		}
		programs = append(programs, prog)
		for range v.Int("projects") {
			pj, err := s.project(ctx, f, prog)
			if err != nil {
				return "", err
			}
			projects = append(projects, pj)
			for range v.Int("assets") {
				in := f.Asset(int16(f.Int(1, 3)))
				lang := fake.Pick(f, "nb", "en", "de")
				in.OriginalLanguage = &lang
				_, version, err := s.filmedAsset(ctx, f, pj.ID, in, locations[:f.Int(1, 2)]...)
				if err != nil {
					return "", err
				}
				assets++
				if !v.Bool("subtitles") {
					continue
				}
				for _, language := range []string{lang, "en"}[:f.Int(1, 2)] {
					if _, err := s.importSRT(ctx, version.ID, language, f.SRT(f.Int(5, 40))); err != nil {
						return "", err
					}
				}
			}
		}
	}
	for range v.Int("users") {
		u, err := s.access.CreateUser(ctx, f.User())
		if err != nil {
			return "", err
		}
		grant := access.GrantInput{UserID: &u.ID, Level: fake.Pick(f, access.View, access.Download, access.Edit, access.Admin)}
		if f.Int(0, 1) == 0 {
			grant.Scope, grant.ScopeID = sqlc.GrantScopeProgram, fake.Pick(f, programs...).ID
		} else {
			grant.Scope, grant.ScopeID = sqlc.GrantScopeProject, fake.Pick(f, projects...).ID
		}
		if _, err := s.access.Grant(ctx, grant); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("created %d programs, %d projects, %d assets, %d users",
		len(programs), len(projects), assets, v.Int("users")), nil
}

func runAccess(ctx context.Context, env Env, _ Values) (string, error) {
	s, f := newServices(env.Q), env.Fake
	users := map[string]sqlc.AppUser{}
	for _, name := range []string{"admin", "editor", "viewer", "downloader", "member", "expired", "inactive", "nobody"} {
		u, err := s.access.CreateUser(ctx, access.UserInput{
			ExternalID: "persona|" + name, Email: name + "@example.test", Name: strings.ToUpper(name[:1]) + name[1:],
			Admin: name == "admin", Inactive: name == "inactive",
		})
		if err != nil {
			return "", err
		}
		users[name] = u
	}
	group, err := s.access.CreateGroup(ctx, "Project 2 crew", []string{users["member"].ID})
	if err != nil {
		return "", err
	}
	locations, err := s.locations(ctx, f, 1)
	if err != nil {
		return "", err
	}
	prog, err := s.program(ctx, f)
	if err != nil {
		return "", err
	}
	var projects []sqlc.Project
	var first sqlc.Asset
	for range 2 {
		pj, err := s.project(ctx, f, prog)
		if err != nil {
			return "", err
		}
		projects = append(projects, pj)
		for range 3 {
			a, version, err := s.filmedAsset(ctx, f, pj.ID, f.Asset(1), locations...)
			if err != nil {
				return "", err
			}
			if first.ID == "" {
				first = a
			}
			if _, err := s.importSRT(ctx, version.ID, "en", f.SRT(10)); err != nil {
				return "", err
			}
		}
	}
	userGrant := func(name string, scope sqlc.GrantScope, id string, level sqlc.PermissionLevel) access.GrantInput {
		u := users[name]
		return access.GrantInput{UserID: &u.ID, Scope: scope, ScopeID: id, Level: level}
	}
	grants := []access.GrantInput{
		userGrant("editor", sqlc.GrantScopeProgram, prog.ID, access.Edit),
		userGrant("viewer", sqlc.GrantScopeProject, projects[0].ID, access.View),
		userGrant("downloader", sqlc.GrantScopeAsset, first.ID, access.Download),
		userGrant("inactive", sqlc.GrantScopeProgram, prog.ID, access.Admin),
		{GroupID: &group.ID, Scope: sqlc.GrantScopeProject, ScopeID: projects[1].ID, Level: access.View},
	}
	for _, g := range grants {
		if _, err := s.access.Grant(ctx, g); err != nil {
			return "", err
		}
	}
	// The service refuses grants that are already expired, so this one is written directly.
	past := time.Now().Add(-time.Hour)
	expired := users["expired"]
	_, err = env.Q.CreateGrant(ctx, sqlc.CreateGrantParams{
		UserID: &expired.ID, Scope: sqlc.GrantScopeProgram, ScopeID: prog.ID, Level: access.Edit, ExpiresAt: &past,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("created %d personas, 1 program, 2 projects, 6 assets, %d grants", len(users), len(grants)+1), nil
}

func runSubtitleRevisions(ctx context.Context, env Env, v Values) (string, error) {
	s, f := newServices(env.Q), env.Fake
	prog, err := s.program(ctx, f)
	if err != nil {
		return "", err
	}
	pj, err := s.project(ctx, f, prog)
	if err != nil {
		return "", err
	}
	a, version, err := s.filmedAsset(ctx, f, pj.ID, f.Asset(2))
	if err != nil {
		return "", err
	}
	content := f.VTT(v.Int("cues"))
	source, err := s.files.Register(ctx, f.SubtitleFile(content))
	if err != nil {
		return "", err
	}
	language := "en"
	track, err := s.subtitles.Import(ctx, subtitle.ImportInput{
		VersionID: version.ID, Language: &language, Format: subtitle.VTT, Content: content, SourceFileID: &source.ID,
	})
	if err != nil {
		return "", err
	}
	for range v.Int("edits") {
		if track, err = s.subtitles.Edit(ctx, track.ID, f.Cues(v.Int("cues"))); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("asset %s has subtitle revision %d", a.ID, track.Revision), nil
}

func edgeCaseTitles() []string {
	return []string{
		"Ærøskøbing – Ünïcødé title",
		"日本語のタイトル",
		"🎬📼 Emoji reel 🎞️",
		strings.Repeat("very long title ", 60),
		"<script>alert('xss')</script>",
		"'; DROP TABLE asset; --",
		"   padded with spaces   ",
		"x",
	}
}

func runEdgeCases(ctx context.Context, env Env, _ Values) (string, error) {
	s, f := newServices(env.Q), env.Fake
	prog, err := s.program(ctx, f)
	if err != nil {
		return "", err
	}
	pj, err := s.project(ctx, f, prog)
	if err != nil {
		return "", err
	}
	locations, err := s.locations(ctx, f, 3)
	if err != nil {
		return "", err
	}

	// Titles; each asset's file sits in all three locations.
	titles := edgeCaseTitles()
	var version sqlc.AssetVersion
	for _, title := range titles {
		in := f.Asset(1)
		in.Title = title
		if _, version, err = s.filmedAsset(ctx, f, pj.ID, in, locations...); err != nil {
			return "", err
		}
	}

	// Structure: no versions at all, and a version without renditions.
	if _, err := s.assets.Create(ctx, f.Asset(3), &pj.ID); err != nil {
		return "", err
	}
	bare, err := s.assets.Create(ctx, f.Asset(2), &pj.ID)
	if err != nil {
		return "", err
	}
	if _, err := s.assets.CreateVersion(ctx, bare.ID, asset.VersionInput{}); err != nil {
		return "", err
	}

	// Files: empty content, and copies that went missing or no longer match.
	empty, err := s.files.Register(ctx, file.RegisterInput{Hashes: f.Hashes()})
	if err != nil {
		return "", err
	}
	for i, status := range []sqlc.CopyStatus{sqlc.CopyStatusMissing, sqlc.CopyStatusMismatch} {
		c, err := s.files.AddCopy(ctx, empty.ID, file.CopyInput{LocationID: locations[i].ID, Path: f.Path("bin")})
		if err != nil {
			return "", err
		}
		if _, err := s.files.SetCopyStatus(ctx, c.ID, status); err != nil {
			return "", err
		}
	}

	// Subtitles: overlapping cues, one very long cue, and the special language codes.
	overlapping := []subtitle.Cue{
		{StartMs: 0, EndMs: 5000, Text: "First speaker keeps talking"},
		{StartMs: 2000, EndMs: 4000, Text: "Second speaker interrupts"},
		{StartMs: 4500, EndMs: 9000, Text: strings.Repeat("A very long cue. ", 200)},
		{StartMs: 86_400_000, EndMs: 86_401_000, Text: "A cue 24 hours in"},
	}
	for _, language := range []string{"zxx", "und"} {
		text, _ := subtitle.Render(subtitle.SRT, overlapping)
		if _, err := s.importSRT(ctx, version.ID, language, text); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("created %d assets with awkward titles, 2 structural edge cases, 1 empty file and 2 subtitle tracks", len(titles)), nil
}
