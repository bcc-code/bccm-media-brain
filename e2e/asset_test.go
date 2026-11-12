//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/bcc-media/mediabrain/internal/fake"
)

const (
	createAsset = `mutation($p: ID, $in: AssetInput!) { createAsset(projectId: $p, input: $in) {
		id typeId title description capturedAt camera reel metadata legacyIds originalLanguage myLevel
		type { name } projects { projectId role linkOrigin project { id } } versions { nodes { id } }
	} }`
	updateAsset = `mutation($id: ID!, $in: AssetInput!) { updateAsset(id: $id, input: $in) { id title description legacyIds metadata updatedAt createdAt } }`
	assetTree   = `query($id: ID!) { asset(id: $id) {
		id title
		versions { nodes { id number label status createdBy
			renditions { id kind language purpose
				files { role position file { id sizeBytes hashes { algo } copies { path location { name } } } }
				subtitleTracks { id revision } } } }
	} }`
)

func TestAssetLifecycle(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(6)
	prog := newProgram(t, admin, f)
	project := newProject(t, admin, f, prog)
	loc := newLocation(t, admin, f)

	in := assetVars(f)
	var created struct {
		CreateAsset struct {
			ID, Title, MyLevel string
			TypeID             int
			LegacyIds          []string
			OriginalLanguage   string
			Type               struct{ Name string }
			Projects           []struct {
				ProjectID, Role, LinkOrigin string
				Project                     struct{ ID string }
			}
			Versions nodes[struct{ ID string }]
		}
	}
	admin.must(t, createAsset, map[string]any{"p": project, "in": in}, &created)
	a := created.CreateAsset
	if !strings.HasPrefix(a.ID, "AS-") || a.Title != in["title"] || a.Type.Name != "Raw material" || a.OriginalLanguage != "en" ||
		len(a.LegacyIds) != 1 || a.MyLevel != "ADMIN" || len(a.Versions) != 0 {
		t.Fatalf("created = %+v", a)
	}
	if len(a.Projects) != 1 || a.Projects[0].Role != "SOURCE" || a.Projects[0].LinkOrigin != "MANUAL" || a.Projects[0].Project.ID != project {
		t.Fatalf("project link = %+v", a.Projects)
	}

	var up struct {
		UpdateAsset struct {
			Title, UpdatedAt, CreatedAt string
			Description                 *string
			LegacyIds                   []string
		}
	}
	admin.must(t, updateAsset, map[string]any{"id": a.ID, "in": map[string]any{"typeId": 2, "title": "New title"}}, &up)
	if u := up.UpdateAsset; u.Title != "New title" || u.Description != nil || len(u.LegacyIds) != 0 || u.UpdatedAt == u.CreatedAt {
		t.Fatalf("updated = %+v", u)
	}

	// Versions are numbered per asset.
	const createVersion = `mutation($a: ID!, $in: VersionInput!) { createAssetVersion(assetId: $a, input: $in) { id number label status createdBy } }`
	type versionJSON struct {
		ID, Status string
		Number     int
		Label      *string
		CreatedBy  *string
	}
	var v1, v2 struct{ CreateAssetVersion versionJSON }
	admin.must(t, createVersion, map[string]any{"a": a.ID, "in": map[string]any{"label": "Rough cut"}}, &v1)
	admin.must(t, createVersion, map[string]any{"a": a.ID, "in": map[string]any{"status": "REVIEW"}}, &v2)
	if v := v1.CreateAssetVersion; v.Number != 1 || v.Status != "DRAFT" || *v.Label != "Rough cut" || *v.CreatedBy != admin.id {
		t.Fatalf("v1 = %+v", v)
	}
	if v := v2.CreateAssetVersion; v.Number != 2 || v.Status != "REVIEW" || v.Label != nil {
		t.Fatalf("v2 = %+v", v)
	}
	var uv struct{ UpdateAssetVersion versionJSON }
	admin.must(t, `mutation($id: ID!) { updateAssetVersion(id: $id, input: {label: "Final", status: FINAL}) { id number label status } }`,
		map[string]any{"id": v2.CreateAssetVersion.ID}, &uv)
	if uv.UpdateAssetVersion.Status != "FINAL" || *uv.UpdateAssetVersion.Label != "Final" {
		t.Fatalf("updated version = %+v", uv.UpdateAssetVersion)
	}

	// Renditions and their files.
	version := v1.CreateAssetVersion.ID
	var r struct {
		CreateRendition struct {
			ID, Kind, Language string
			Overrides          map[string]any
		}
	}
	admin.must(t, `mutation($v: ID!) { createRendition(versionId: $v, input: {kind: DUB, language: "de", overrides: {title: "Titel"}}) { id kind language overrides } }`,
		map[string]any{"v": version}, &r)
	if r.CreateRendition.Kind != "DUB" || r.CreateRendition.Language != "de" || r.CreateRendition.Overrides["title"] != "Titel" {
		t.Fatalf("rendition = %+v", r.CreateRendition)
	}
	rendition := r.CreateRendition.ID
	var ur struct {
		UpdateRendition struct {
			Kind     string
			Language *string
			Purpose  string
		}
	}
	admin.must(t, `mutation($id: ID!) { updateRendition(id: $id, input: {purpose: "broadcast"}) { kind language purpose } }`, map[string]any{"id": rendition}, &ur)
	if ur.UpdateRendition.Kind != "DUB" || ur.UpdateRendition.Language != nil || ur.UpdateRendition.Purpose != "broadcast" {
		t.Fatalf("updated rendition = %+v", ur.UpdateRendition)
	}

	video, audio := newFile(t, admin, f, loc), newFile(t, admin, f, loc)
	const attachFile = `mutation($r: ID!, $f: ID!, $role: FileRole!, $pos: Int!) { attachFile(renditionId: $r, fileId: $f, role: $role, position: $pos) { role position file { id } } }`
	admin.must(t, attachFile, map[string]any{"r": rendition, "f": video, "role": "VIDEO", "pos": 0}, nil)
	admin.must(t, attachFile, map[string]any{"r": rendition, "f": audio, "role": "AUDIO", "pos": 1}, nil)
	admin.fails(t, "CONFLICT", attachFile, map[string]any{"r": rendition, "f": video, "role": "VIDEO", "pos": 5})
	admin.fails(t, "INVALID_INPUT", attachFile, map[string]any{"r": rendition, "f": "FI-00000000000000000000000000", "role": "VIDEO", "pos": 0})

	type treeJSON struct {
		Versions nodes[struct {
			ID         string
			Number     int
			Renditions []struct {
				ID    string
				Files []struct {
					Role string
					File struct {
						ID     string
						Hashes []struct{ Algo string }
						Copies []struct {
							Path     string
							Location struct{ Name string }
						}
					}
				}
			}
		}]
	}
	var tree treeJSON
	admin.must(t, assetTree, map[string]any{"id": a.ID}, &struct{ Asset *treeJSON }{&tree})
	if len(tree.Versions) != 2 || tree.Versions[0].Number != 1 || len(tree.Versions[0].Renditions) != 1 || len(tree.Versions[1].Renditions) != 0 {
		t.Fatalf("tree = %+v", tree)
	}
	files := tree.Versions[0].Renditions[0].Files
	if len(files) != 2 || files[0].Role != "VIDEO" || files[0].File.ID != video || files[1].File.ID != audio ||
		len(files[0].File.Hashes) != 2 || len(files[0].File.Copies) != 1 || files[0].File.Copies[0].Location.Name == "" {
		t.Fatalf("files = %+v", files)
	}

	// Lookups by id of the parts.
	admin.sees(t, "assetVersion", version)
	admin.sees(t, "rendition", rendition)

	const detach = `mutation($r: ID!, $f: ID!) { detachFile(renditionId: $r, fileId: $f, role: AUDIO) }`
	admin.must(t, detach, map[string]any{"r": rendition, "f": audio}, nil)
	admin.fails(t, "NOT_FOUND", detach, map[string]any{"r": rendition, "f": audio})

	// A file in use cannot be deleted, and deleting the asset leaves it in place.
	admin.fails(t, "CONFLICT", `mutation($id: ID!) { deleteFile(id: $id) }`, map[string]any{"id": video})
	admin.must(t, `mutation($id: ID!) { deleteAssetVersion(id: $id) }`, map[string]any{"id": v2.CreateAssetVersion.ID}, nil)
	admin.isNull(t, "assetVersion", v2.CreateAssetVersion.ID)
	admin.must(t, `mutation($id: ID!) { deleteAsset(id: $id) }`, map[string]any{"id": a.ID}, nil)
	for field, id := range map[string]string{"asset": a.ID, "assetVersion": version, "rendition": rendition} {
		admin.isNull(t, field, id)
	}
	admin.sees(t, "file", video)
	admin.fails(t, "NOT_FOUND", `mutation($id: ID!) { deleteAsset(id: $id) }`, map[string]any{"id": a.ID})

	// A second rendition, deleted on its own.
	other := newClip(t, admin, f, project, loc)
	admin.must(t, `mutation($id: ID!) { deleteRendition(id: $id) }`, map[string]any{"id": other.rendition}, nil)
	admin.isNull(t, "rendition", other.rendition)
}

func TestAssetListAndFilters(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(7)
	prog := newProgram(t, admin, f)
	p1, p2 := newProject(t, admin, f, prog), newProject(t, admin, f, prog)

	marker := "Marker" + unique()
	create := func(project string, patch map[string]any) string {
		in := assetVars(f)
		for k, v := range patch {
			in[k] = v
		}
		var out struct{ CreateAsset struct{ ID string } }
		admin.must(t, createAsset, map[string]any{"p": project, "in": in}, &out)
		return out.CreateAsset.ID
	}
	legacy := "OLD-" + unique()
	a1 := create(p1, map[string]any{"title": marker + " one", "typeId": 1})
	a2 := create(p1, map[string]any{"title": "two " + strings.ToLower(marker), "typeId": 2, "legacyIds": []string{legacy, "x"}})
	a3 := create(p2, map[string]any{"title": marker + " three", "typeId": 2})

	// ids lists a page; the cursor it leaves in next leads to the page after it.
	var next *string
	ids := func(filter map[string]any, first int, after *string) []string {
		var out struct {
			Assets struct {
				Nodes    []struct{ ID string }
				PageInfo pageInfo
			}
		}
		admin.must(t, `query($f: AssetFilter, $n: Int, $c: String) { assets(filter: $f, first: $n, after: $c) { nodes { id } pageInfo { hasNextPage endCursor } } }`,
			map[string]any{"f": filter, "n": first, "c": after}, &out)
		got := make([]string, len(out.Assets.Nodes))
		for i, a := range out.Assets.Nodes {
			got[i] = a.ID
		}
		next = out.Assets.PageInfo.EndCursor
		return got
	}
	eq := func(name string, got []string, want ...string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	eq("title, newest first", ids(map[string]any{"title": marker}, 50, nil), a3, a2, a1)
	eq("project", ids(map[string]any{"projectId": p1, "title": marker}, 50, nil), a2, a1)
	eq("type", ids(map[string]any{"typeId": 2, "title": marker}, 50, nil), a3, a2)
	eq("legacy id", ids(map[string]any{"legacyId": legacy}, 50, nil), a2)
	eq("page 1", ids(map[string]any{"title": marker}, 2, nil), a3, a2)
	eq("page 2", ids(map[string]any{"title": marker}, 2, next), a1)
	eq("nothing", ids(map[string]any{"title": "no such title " + unique()}, 50, nil))
	if len(ids(nil, 1, nil)) != 1 {
		t.Error("no filter should list assets")
	}

	// Linking to a second project and unlinking again.
	const link = `mutation($a: ID!, $p: ID!, $r: LinkRole!) { linkAssetToProject(assetId: $a, projectId: $p, role: $r) { projectId role } }`
	const unlink = `mutation($a: ID!, $p: ID!, $r: LinkRole!) { unlinkAssetFromProject(assetId: $a, projectId: $p, role: $r) }`
	vars := map[string]any{"a": a1, "p": p2, "r": "REFERENCE"}
	admin.must(t, link, vars, nil)
	admin.fails(t, "CONFLICT", link, vars)
	eq("after link", ids(map[string]any{"projectId": p2, "title": marker}, 50, nil), a3, a1)
	admin.must(t, unlink, vars, nil)
	admin.fails(t, "NOT_FOUND", unlink, vars)
	eq("after unlink", ids(map[string]any{"projectId": p2, "title": marker}, 50, nil), a3)
	admin.fails(t, "NOT_FOUND", link, map[string]any{"a": a1, "p": "PJ-00000000000000000000000000", "r": "SOURCE"})
	admin.fails(t, "NOT_FOUND", link, map[string]any{"a": "AS-00000000000000000000000000", "p": p2, "r": "SOURCE"})

	// Deleting a project removes the links, not the assets.
	admin.must(t, `mutation($id: ID!) { deleteProject(id: $id) }`, map[string]any{"id": p2}, nil)
	admin.sees(t, "asset", a3)

	var ref struct {
		AssetTypes []struct{ Name string }
		Languages  []struct{ Code string }
		All        []struct{ Code string }
	}
	admin.must(t, `{ assetTypes { id name } languages { code name modality } all: languages(activeOnly: false) { code } }`, nil, &ref)
	if len(ref.AssetTypes) != 3 || len(ref.Languages) < 20 || len(ref.All) < len(ref.Languages) {
		t.Fatalf("reference data: %d types, %d languages", len(ref.AssetTypes), len(ref.Languages))
	}
}

func TestAssetValidation(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(8)
	prog := newProgram(t, admin, f)
	project := newProject(t, admin, f, prog)
	c := newClip(t, admin, f, project, "")

	withAsset := func(patch map[string]any) map[string]any {
		in := assetVars(f)
		for k, v := range patch {
			in[k] = v
		}
		return map[string]any{"p": project, "in": in}
	}
	for name, vars := range map[string]map[string]any{
		"blank title":      withAsset(map[string]any{"title": "   "}),
		"unknown type":     withAsset(map[string]any{"typeId": 999}),
		"unknown language": withAsset(map[string]any{"originalLanguage": "xx-nope"}),
		"metadata array":   withAsset(map[string]any{"metadata": []int{1}}),
	} {
		t.Run(name, func(t *testing.T) { admin.fails(t, "INVALID_INPUT", createAsset, vars) })
	}
	admin.fails(t, "NOT_FOUND", createAsset, map[string]any{"p": "PJ-00000000000000000000000000", "in": assetVars(f)})
	admin.fails(t, "INVALID_INPUT", updateAsset, map[string]any{"id": c.asset, "in": map[string]any{"typeId": 1, "title": ""}})
	admin.fails(t, "NOT_FOUND", updateAsset, map[string]any{"id": "AS-00000000000000000000000000", "in": map[string]any{"typeId": 1, "title": "x"}})
	admin.fails(t, "INVALID_INPUT", `{ assets(first: 501) { nodes { id } } }`, nil)

	missing := map[string]any{"id": "AV-00000000000000000000000000"}
	admin.fails(t, "NOT_FOUND", `mutation($id: ID!) { createAssetVersion(assetId: $id, input: {}) { id } }`, map[string]any{"id": "AS-00000000000000000000000000"})
	admin.fails(t, "NOT_FOUND", `mutation($id: ID!) { updateAssetVersion(id: $id, input: {}) { id } }`, missing)
	admin.fails(t, "NOT_FOUND", `mutation($id: ID!) { deleteAssetVersion(id: $id) }`, missing)
	admin.fails(t, "NOT_FOUND", `mutation($id: ID!) { createRendition(versionId: $id, input: {kind: MASTER}) { id } }`, missing)
	admin.fails(t, "NOT_FOUND", `mutation($id: ID!) { updateRendition(id: $id, input: {}) { id } }`, map[string]any{"id": "RE-00000000000000000000000000"})
	admin.fails(t, "NOT_FOUND", `mutation($id: ID!) { deleteRendition(id: $id) }`, map[string]any{"id": "RE-00000000000000000000000000"})
	admin.fails(t, "INVALID_INPUT", `mutation($id: ID!) { createRendition(versionId: $id, input: {kind: DUB, language: "nope"}) { id } }`, map[string]any{"id": c.version})
	admin.fails(t, "INVALID_INPUT", `mutation($id: ID!) { createRendition(versionId: $id, input: {kind: DUB, overrides: [1]}) { id } }`, map[string]any{"id": c.version})
	admin.fails(t, "INVALID_INPUT", `mutation($id: ID!) { updateRendition(id: $id, input: {metadata: "x"}) { id } }`, map[string]any{"id": c.rendition})

	// An asset used by a timeline cannot be deleted.
	ctx := context.Background()
	var track string
	err := pool.QueryRow(ctx, `WITH tl AS (INSERT INTO timeline (project_id, name, rate_num) VALUES ($1, 'cut', 25) RETURNING id)
		INSERT INTO timeline_track (timeline_id, kind, position) SELECT id, 'video', 0 FROM tl RETURNING id`, project).Scan(&track)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO timeline_clip (track_id, asset_id, source_out, record_in) VALUES ($1, $2, 10, 0)`, track, c.asset); err != nil {
		t.Fatal(err)
	}
	admin.fails(t, "CONFLICT", `mutation($id: ID!) { deleteAsset(id: $id) }`, map[string]any{"id": c.asset})
}
