//go:build e2e

package e2e

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/fake"
)

// world is one program with two projects. Project A holds c1 and c2, project B holds c3.
type world struct {
	admin              user
	f                  *fake.Faker
	marker             string // part of every asset title, to find them in lists
	prog               program
	projectA, projectB string
	location           string
	c1, c2, c3         clip
	track              string // subtitles on c1
}

func newWorld(t *testing.T, seed uint64) world {
	t.Helper()
	w := world{admin: signInAdmin(t), f: fake.New(seed), marker: "W" + unique()}
	w.prog = newProgram(t, w.admin, w.f)
	w.projectA, w.projectB = newProject(t, w.admin, w.f, w.prog), newProject(t, w.admin, w.f, w.prog)
	w.location = newLocation(t, w.admin, w.f)
	w.c1 = newClip(t, w.admin, w.f, w.projectA, w.location)
	w.c2 = newClip(t, w.admin, w.f, w.projectA, w.location)
	w.c3 = newClip(t, w.admin, w.f, w.projectB, w.location)
	for _, c := range []clip{w.c1, w.c2, w.c3} {
		w.admin.must(t, updateAsset, map[string]any{"id": c.asset, "in": map[string]any{"typeId": 1, "title": w.marker + " " + c.asset}}, nil)
	}
	var out struct{ ImportSubtitles trackJSON }
	w.admin.must(t, importSubtitles, map[string]any{"in": map[string]any{
		"versionId": w.c1.version, "language": "en", "format": "SRT", "content": w.f.SRT(3)}}, &out)
	w.track = out.ImportSubtitles.ID
	return w
}

// visible returns which of the world's assets the user finds in the asset list.
func (w world) visible(t *testing.T, u user) string {
	t.Helper()
	var out struct{ Assets []struct{ ID string } }
	u.must(t, `query($t: String) { assets(filter: {title: $t}, limit: 500) { id } }`, map[string]any{"t": w.marker}, &out)
	names := map[string]string{w.c1.asset: "c1", w.c2.asset: "c2", w.c3.asset: "c3"}
	var got []string
	for _, a := range out.Assets {
		got = append(got, names[a.ID])
	}
	sort.Strings(got)
	return strings.Join(got, ",")
}

func TestNoAccessLooksLikeNotFound(t *testing.T) {
	w := newWorld(t, 20)
	nobody := signIn(t)

	if got := w.visible(t, nobody); got != "" {
		t.Fatalf("assets listed for a user without grants: %s", got)
	}
	for field, id := range map[string]string{
		"asset": w.c1.asset, "assetVersion": w.c1.version, "rendition": w.c1.rendition, "file": w.c1.file,
		"subtitleTrack": w.track, "project": w.projectA, "program": w.prog.ID,
	} {
		nobody.isNull(t, field, id)
	}
	var lists struct {
		Programs []struct{ ID string }
		Projects []struct{ ID string }
	}
	nobody.must(t, `query($p: ID) { programs(limit: 500) { id } projects(programId: $p) { id } }`, map[string]any{"p": w.prog.ID}, &lists)
	if len(lists.Programs) != 0 || len(lists.Projects) != 0 {
		t.Fatalf("lists = %+v", lists)
	}

	// Every operation answers exactly as it does for an id that does not exist, so the
	// response cannot be used to probe for objects.
	ghost := map[string]string{"AS": "AS-00000000000000000000000000", "AV": "AV-00000000000000000000000000",
		"RE": "RE-00000000000000000000000000", "ST": "ST-00000000000000000000000000", "PJ": "PJ-00000000000000000000000000",
		"PG": "PG-00000000000000000000000000"}
	cue := []map[string]any{{"startMs": 0, "endMs": 1, "text": "x"}}
	for name, c := range map[string]struct {
		query    string
		real     map[string]any
		notThere map[string]any
	}{
		"update asset":   {updateAsset, map[string]any{"id": w.c1.asset, "in": map[string]any{"typeId": 1, "title": "x"}}, map[string]any{"id": ghost["AS"], "in": map[string]any{"typeId": 1, "title": "x"}}},
		"delete asset":   {`mutation($id: ID!) { deleteAsset(id: $id) }`, map[string]any{"id": w.c1.asset}, map[string]any{"id": ghost["AS"]}},
		"new version":    {`mutation($id: ID!) { createAssetVersion(assetId: $id, input: {}) { id } }`, map[string]any{"id": w.c1.asset}, map[string]any{"id": ghost["AS"]}},
		"delete version": {`mutation($id: ID!) { deleteAssetVersion(id: $id) }`, map[string]any{"id": w.c1.version}, map[string]any{"id": ghost["AV"]}},
		"new rendition":  {`mutation($id: ID!) { createRendition(versionId: $id, input: {kind: MASTER}) { id } }`, map[string]any{"id": w.c1.version}, map[string]any{"id": ghost["AV"]}},
		"detach file":    {`mutation($id: ID!, $f: ID!) { detachFile(renditionId: $id, fileId: $f, role: VIDEO) }`, map[string]any{"id": w.c1.rendition, "f": w.c1.file}, map[string]any{"id": ghost["RE"], "f": w.c1.file}},
		"edit subtitles": {editSubtitles, map[string]any{"id": w.track, "cues": cue}, map[string]any{"id": ghost["ST"], "cues": cue}},
		"delete track":   {`mutation($id: ID!) { deleteSubtitleTrack(id: $id) }`, map[string]any{"id": w.track}, map[string]any{"id": ghost["ST"]}},
		"list tracks":    {`query($id: ID!) { subtitleTracks(assetId: $id) { id } }`, map[string]any{"id": w.c1.asset}, map[string]any{"id": ghost["AS"]}},
		"update project": {updateProject, map[string]any{"id": w.projectA, "in": map[string]any{"name": "x"}}, map[string]any{"id": ghost["PJ"], "in": map[string]any{"name": "x"}}},
		"delete project": {`mutation($id: ID!) { deleteProject(id: $id) }`, map[string]any{"id": w.projectA}, map[string]any{"id": ghost["PJ"]}},
		"update program": {`mutation($id: ID!) { updateProgram(id: $id, input: {name: "x"}) { id } }`, map[string]any{"id": w.prog.ID}, map[string]any{"id": ghost["PG"]}},
		"list grants":    {`query($id: ID!) { grants(scope: ASSET, scopeId: $id) { id } }`, map[string]any{"id": w.c1.asset}, map[string]any{"id": ghost["AS"]}},
		"grant":          {grantAccess, map[string]any{"in": map[string]any{"scope": "ASSET", "scopeId": w.c1.asset, "level": "VIEW", "userId": nobody.id}}, map[string]any{"in": map[string]any{"scope": "ASSET", "scopeId": ghost["AS"], "level": "VIEW", "userId": nobody.id}}},
		"create asset":   {createAsset, map[string]any{"p": w.projectA, "in": assetVars(w.f)}, map[string]any{"p": ghost["PJ"], "in": assetVars(w.f)}},
	} {
		t.Run(name, func(t *testing.T) {
			hidden := nobody.fails(t, "NOT_FOUND", c.query, c.real)
			missing := nobody.fails(t, "NOT_FOUND", c.query, c.notThere)
			if hidden.Message != missing.Message {
				t.Fatalf("hidden object answered %q, missing object %q", hidden.Message, missing.Message)
			}
		})
	}
	// Nothing was changed by any of that.
	w.admin.sees(t, "asset", w.c1.asset)
	w.admin.sees(t, "subtitleTrack", w.track)

	// A user without a project cannot create assets at all.
	nobody.fails(t, "INVALID_INPUT", createAsset, map[string]any{"in": assetVars(w.f)})
}

func TestAdminOnlyOperations(t *testing.T) {
	w := newWorld(t, 21)
	editor := signIn(t)
	grant(t, w.admin, "PROGRAM", w.prog.ID, "ADMIN", to(editor)) // the highest grant is still not a system administrator

	loc := map[string]any{"name": "x" + unique(), "kind": "NAS", "baseUri": "smb://x"}
	for name, c := range map[string]struct {
		query string
		vars  map[string]any
	}{
		"create program":    {createProgram, map[string]any{"in": map[string]any{"code": "QQQQ", "name": "x"}}},
		"delete program":    {`mutation($id: ID!) { deleteProgram(id: $id) }`, map[string]any{"id": w.prog.ID}},
		"create location":   {`mutation($in: StorageLocationInput!) { createStorageLocation(input: $in) { id } }`, map[string]any{"in": loc}},
		"update location":   {`mutation($id: ID!, $in: StorageLocationInput!) { updateStorageLocation(id: $id, input: $in) { id } }`, map[string]any{"id": w.location, "in": loc}},
		"delete location":   {`mutation($id: ID!) { deleteStorageLocation(id: $id) }`, map[string]any{"id": w.location}},
		"list locations":    {`{ storageLocations { id baseUri } }`, nil},
		"register file":     {registerFile, map[string]any{"in": fileVars(w.f.VideoFile())}},
		"list files":        {`{ files { id } }`, nil},
		"add copy":          {`mutation($f: ID!, $c: FileCopyInput!) { addFileCopy(fileId: $f, copy: $c) { id } }`, map[string]any{"f": w.c1.file, "c": map[string]any{"locationId": w.location, "path": "x.mov"}}},
		"copy status":       {`mutation($id: ID!) { setFileCopyStatus(id: $id, status: MISSING) { id } }`, map[string]any{"id": "FC-00000000000000000000000000"}},
		"remove copy":       {`mutation($id: ID!) { removeFileCopy(id: $id) }`, map[string]any{"id": "FC-00000000000000000000000000"}},
		"delete file":       {`mutation($id: ID!) { deleteFile(id: $id) }`, map[string]any{"id": w.c1.file}},
		"list users":        {`{ users { id email } }`, nil},
		"list groups":       {`{ userGroups { id } }`, nil},
		"make admin":        {`mutation($u: String!) { setUserAdmin(user: $u, admin: true) { id } }`, map[string]any{"u": editor.id}},
		"vidispine import":  {importFromVidispine, map[string]any{"in": map[string]any{"itemId": vidispineItem(w.f, newVidispineStorage()).ID, "typeId": 1, "projectId": w.projectA}}},
		"vidispine refresh": {refreshFromVidispine, map[string]any{"id": w.c1.asset}},
	} {
		t.Run(name, func(t *testing.T) { editor.fails(t, "FORBIDDEN", c.query, c.vars) })
	}
	w.admin.sees(t, "file", w.c1.file)
}

func TestPermissionLevels(t *testing.T) {
	w := newWorld(t, 22)
	viewer, downloader, editor, deleter, owner := signIn(t), signIn(t), signIn(t), signIn(t), signIn(t)
	grant(t, w.admin, "PROJECT", w.projectA, "VIEW", to(viewer))
	grant(t, w.admin, "ASSET", w.c1.asset, "DOWNLOAD_ORIGINAL", to(downloader))
	grant(t, w.admin, "PROJECT", w.projectA, "EDIT", to(editor))
	grant(t, w.admin, "ASSET", w.c1.asset, "DELETE", to(deleter))
	grant(t, w.admin, "ASSET", w.c1.asset, "ADMIN", to(owner))

	for name, c := range map[string]struct {
		u    user
		want string
	}{"viewer": {viewer, "c1,c2"}, "downloader": {downloader, "c1"}, "editor": {editor, "c1,c2"}, "deleter": {deleter, "c1"}, "owner": {owner, "c1"}, "admin": {w.admin, "c1,c2,c3"}} {
		if got := w.visible(t, c.u); got != c.want {
			t.Errorf("%s sees %q, want %q", name, got, c.want)
		}
	}

	type view struct {
		Asset *struct {
			MyLevel  string
			Projects []struct{ Project *struct{ ID string } }
			Versions []struct {
				Renditions []struct {
					Kind  string
					Files []struct {
						File struct {
							ID     string
							Hashes []struct{ Algo string }
							Copies []struct {
								Path     string
								Location struct{ BaseUri string }
							}
						}
					}
					SubtitleTracks []struct {
						ID   string
						Cues []struct{ Text string }
					}
				}
			}
		}
	}
	const viewQuery = `query($id: ID!) { asset(id: $id) { myLevel projects { project { id } }
		versions { renditions { kind files { file { id hashes { algo } copies { path location { baseUri } } } } subtitleTracks { id cues { text } } } } } }`
	look := func(u user, asset string) view {
		var v view
		u.must(t, viewQuery, map[string]any{"id": asset}, &v)
		return v
	}
	masterFile := func(v view) (id string, hashes, copies int) {
		for _, r := range v.Asset.Versions[0].Renditions {
			if r.Kind == "MASTER" {
				return r.Files[0].File.ID, len(r.Files[0].File.Hashes), len(r.Files[0].File.Copies)
			}
		}
		t.Fatal("no master rendition")
		return
	}

	t.Run("view", func(t *testing.T) {
		v := look(viewer, w.c1.asset)
		if v.Asset.MyLevel != "VIEW" || v.Asset.Projects[0].Project.ID != w.projectA {
			t.Fatalf("asset = %+v", v.Asset)
		}
		// File metadata is visible; where the originals are stored is not.
		if id, hashes, copies := masterFile(v); id != w.c1.file || hashes != 2 || copies != 0 {
			t.Fatalf("file %s: %d hashes, %d copies", id, hashes, copies)
		}
		subtitles := 0
		for _, r := range v.Asset.Versions[0].Renditions {
			for _, tr := range r.SubtitleTracks {
				subtitles += len(tr.Cues)
			}
		}
		if subtitles != 3 {
			t.Fatalf("viewer reads %d cues", subtitles)
		}
		viewer.sees(t, "file", w.c1.file)
		viewer.sees(t, "subtitleTrack", w.track)
		viewer.sees(t, "assetVersion", w.c1.version)
		viewer.sees(t, "rendition", w.c1.rendition)
		viewer.sees(t, "project", w.projectA)
		viewer.isNull(t, "asset", w.c3.asset)
		viewer.isNull(t, "file", w.c3.file)
		viewer.isNull(t, "project", w.projectB)

		// A visible project makes its program visible, with only the projects one may see.
		var prog struct {
			Program  struct{ Projects []struct{ ID string } }
			Programs []struct{ ID string }
			Projects []struct{ ID string }
		}
		viewer.must(t, `query($p: ID!) { program(id: $p) { projects { id } } programs(limit: 500) { id } projects(programId: $p) { id } }`, map[string]any{"p": w.prog.ID}, &prog)
		if len(prog.Program.Projects) != 1 || prog.Program.Projects[0].ID != w.projectA || len(prog.Programs) != 1 || len(prog.Projects) != 1 {
			t.Fatalf("program as seen by viewer = %+v", prog)
		}
		// Finding a file by hash obeys the same rule as finding it by id.
		var hash struct{ FileByHash *struct{ ID string } }
		for file, want := range map[string]bool{w.c1.file: true, w.c3.file: false} {
			var value string
			if err := pool.QueryRow(context.Background(), `SELECT encode(value, 'hex') FROM file_hash WHERE file_id = $1 AND algo = 'sha256'`, file).Scan(&value); err != nil {
				t.Fatal(err)
			}
			viewer.must(t, `query($v: String!) { fileByHash(hash: {algo: SHA256, value: $v}) { id } }`, map[string]any{"v": value}, &hash)
			if (hash.FileByHash != nil) != want {
				t.Fatalf("fileByHash(%s) visible = %v, want %v", file, hash.FileByHash != nil, want)
			}
		}

		cue := []map[string]any{{"startMs": 0, "endMs": 1, "text": "x"}}
		for name, c := range map[string]struct {
			query string
			vars  map[string]any
		}{
			"update asset":     {updateAsset, map[string]any{"id": w.c1.asset, "in": map[string]any{"typeId": 1, "title": "x"}}},
			"delete asset":     {`mutation($id: ID!) { deleteAsset(id: $id) }`, map[string]any{"id": w.c1.asset}},
			"new version":      {`mutation($id: ID!) { createAssetVersion(assetId: $id, input: {}) { id } }`, map[string]any{"id": w.c1.asset}},
			"update version":   {`mutation($id: ID!) { updateAssetVersion(id: $id, input: {}) { id } }`, map[string]any{"id": w.c1.version}},
			"delete version":   {`mutation($id: ID!) { deleteAssetVersion(id: $id) }`, map[string]any{"id": w.c1.version}},
			"new rendition":    {`mutation($id: ID!) { createRendition(versionId: $id, input: {kind: MASTER}) { id } }`, map[string]any{"id": w.c1.version}},
			"update rendition": {`mutation($id: ID!) { updateRendition(id: $id, input: {}) { id } }`, map[string]any{"id": w.c1.rendition}},
			"delete rendition": {`mutation($id: ID!) { deleteRendition(id: $id) }`, map[string]any{"id": w.c1.rendition}},
			"attach file":      {`mutation($r: ID!, $f: ID!) { attachFile(renditionId: $r, fileId: $f, role: AUDIO) { position } }`, map[string]any{"r": w.c1.rendition, "f": w.c2.file}},
			"detach file":      {`mutation($r: ID!, $f: ID!) { detachFile(renditionId: $r, fileId: $f, role: VIDEO) }`, map[string]any{"r": w.c1.rendition, "f": w.c1.file}},
			"import subtitles": {importSubtitles, map[string]any{"in": map[string]any{"versionId": w.c1.version, "format": "SRT", "content": w.f.SRT(1)}}},
			"edit subtitles":   {editSubtitles, map[string]any{"id": w.track, "cues": cue}},
			"delete track":     {`mutation($id: ID!) { deleteSubtitleTrack(id: $id) }`, map[string]any{"id": w.track}},
			"link":             {`mutation($a: ID!, $p: ID!) { linkAssetToProject(assetId: $a, projectId: $p, role: REFERENCE) { role } }`, map[string]any{"a": w.c1.asset, "p": w.projectA}},
			"unlink":           {`mutation($a: ID!, $p: ID!) { unlinkAssetFromProject(assetId: $a, projectId: $p, role: SOURCE) }`, map[string]any{"a": w.c1.asset, "p": w.projectA}},
			"create asset":     {createAsset, map[string]any{"p": w.projectA, "in": assetVars(w.f)}},
			"update project":   {updateProject, map[string]any{"id": w.projectA, "in": map[string]any{"name": "x"}}},
			"delete project":   {`mutation($id: ID!) { deleteProject(id: $id) }`, map[string]any{"id": w.projectA}},
			"create project":   {createProject, map[string]any{"in": projectVars(w.f, w.prog)}},
			"update program":   {`mutation($id: ID!) { updateProgram(id: $id, input: {name: "x"}) { id } }`, map[string]any{"id": w.prog.ID}},
			"list grants":      {`query($id: ID!) { grants(scope: ASSET, scopeId: $id) { id } }`, map[string]any{"id": w.c1.asset}},
			"grant":            {grantAccess, map[string]any{"in": map[string]any{"scope": "PROJECT", "scopeId": w.projectA, "level": "ADMIN", "userId": viewer.id}}},
		} {
			t.Run(name, func(t *testing.T) { viewer.fails(t, "FORBIDDEN", c.query, c.vars) })
		}
	})

	t.Run("download original", func(t *testing.T) {
		v := look(downloader, w.c1.asset)
		if v.Asset.MyLevel != "DOWNLOAD_ORIGINAL" {
			t.Fatalf("level = %s", v.Asset.MyLevel)
		}
		if _, _, copies := masterFile(v); copies != 1 {
			t.Fatalf("downloader sees %d copies, want 1", copies)
		}
		// A grant on an asset does not reveal the project it belongs to.
		if len(v.Asset.Projects) != 0 {
			t.Fatalf("project leaked through asset: %+v", v.Asset.Projects)
		}
		downloader.isNull(t, "project", w.projectA)
		downloader.isNull(t, "program", w.prog.ID)
		downloader.fails(t, "FORBIDDEN", updateAsset, map[string]any{"id": w.c1.asset, "in": map[string]any{"typeId": 1, "title": "x"}})
	})

	t.Run("edit", func(t *testing.T) {
		title := w.marker + " " + w.c2.asset + " edited"
		editor.must(t, updateAsset, map[string]any{"id": w.c2.asset, "in": map[string]any{"typeId": 1, "title": title}}, nil)
		version := newVersion(t, editor, w.c2.asset)
		rendition := newRendition(t, editor, version)
		// Files the editor may download can be attached; files they cannot see do not exist.
		attach(t, editor, rendition, w.c1.file)
		editor.fails(t, "INVALID_INPUT", `mutation($r: ID!, $f: ID!) { attachFile(renditionId: $r, fileId: $f, role: VIDEO) { position } }`,
			map[string]any{"r": rendition, "f": w.c3.file})
		editor.fails(t, "INVALID_INPUT", importSubtitles, map[string]any{"in": map[string]any{
			"versionId": version, "format": "SRT", "content": w.f.SRT(1), "sourceFileId": w.c3.file}})

		var imp struct{ ImportSubtitles trackJSON }
		editor.must(t, importSubtitles, map[string]any{"in": map[string]any{"versionId": version, "language": "de", "format": "SRT", "content": w.f.SRT(2)}}, &imp)
		if *imp.ImportSubtitles.CreatedBy != editor.id {
			t.Fatalf("created by %v", imp.ImportSubtitles.CreatedBy)
		}
		editor.must(t, editSubtitles, map[string]any{"id": imp.ImportSubtitles.ID, "cues": []map[string]any{{"startMs": 0, "endMs": 9, "text": "x"}}}, nil)
		editor.must(t, updateProject, map[string]any{"id": w.projectA, "in": map[string]any{"name": "Edited"}}, nil)

		// New assets land in the project and are visible to everyone with access to it.
		created := newAsset(t, editor, w.f, w.projectA)
		viewer.sees(t, "asset", created)
		editor.fails(t, "INVALID_INPUT", createAsset, map[string]any{"in": assetVars(w.f)})

		// Linking needs edit on both sides; project B is invisible to the editor.
		const link = `mutation($a: ID!, $p: ID!) { linkAssetToProject(assetId: $a, projectId: $p, role: REFERENCE) { role } }`
		editor.fails(t, "NOT_FOUND", link, map[string]any{"a": created, "p": w.projectB})

		for name, c := range map[string]struct {
			query string
			vars  map[string]any
		}{
			"delete asset":     {`mutation($id: ID!) { deleteAsset(id: $id) }`, map[string]any{"id": w.c2.asset}},
			"delete version":   {`mutation($id: ID!) { deleteAssetVersion(id: $id) }`, map[string]any{"id": version}},
			"delete rendition": {`mutation($id: ID!) { deleteRendition(id: $id) }`, map[string]any{"id": rendition}},
			"delete track":     {`mutation($id: ID!) { deleteSubtitleTrack(id: $id) }`, map[string]any{"id": imp.ImportSubtitles.ID}},
			"delete project":   {`mutation($id: ID!) { deleteProject(id: $id) }`, map[string]any{"id": w.projectA}},
			"create project":   {createProject, map[string]any{"in": projectVars(w.f, w.prog)}},
			"grant":            {grantAccess, map[string]any{"in": map[string]any{"scope": "ASSET", "scopeId": w.c2.asset, "level": "VIEW", "userId": viewer.id}}},
		} {
			t.Run(name, func(t *testing.T) { editor.fails(t, "FORBIDDEN", c.query, c.vars) })
		}
	})

	t.Run("delete", func(t *testing.T) {
		deleter.fails(t, "FORBIDDEN", grantAccess, map[string]any{"in": map[string]any{"scope": "ASSET", "scopeId": w.c1.asset, "level": "VIEW", "userId": viewer.id}})
		deleter.must(t, `mutation($id: ID!) { deleteSubtitleTrack(id: $id) }`, map[string]any{"id": w.track}, nil)
		version := newVersion(t, deleter, w.c1.asset)
		rendition := newRendition(t, deleter, version)
		deleter.must(t, `mutation($id: ID!) { deleteRendition(id: $id) }`, map[string]any{"id": rendition}, nil)
		deleter.must(t, `mutation($id: ID!) { deleteAssetVersion(id: $id) }`, map[string]any{"id": version}, nil)
	})

	t.Run("admin on the asset", func(t *testing.T) {
		friend := signIn(t)
		friend.isNull(t, "asset", w.c1.asset)
		id := grant(t, owner, "ASSET", w.c1.asset, "VIEW", map[string]any{"userEmail": strings.ToUpper(friend.email)})
		friend.sees(t, "asset", w.c1.asset)

		var list struct {
			Grants []struct {
				ID, Level string
				UserID    *string
				GrantedBy *string
			}
		}
		owner.must(t, `query($id: ID!) { grants(scope: ASSET, scopeId: $id) { id level userId grantedBy } }`, map[string]any{"id": w.c1.asset}, &list)
		found := false
		for _, g := range list.Grants {
			if g.ID == id {
				found = g.Level == "VIEW" && *g.UserID == friend.id && *g.GrantedBy == owner.id
			}
		}
		if !found || len(list.Grants) != 4 {
			t.Fatalf("grants = %+v", list.Grants)
		}

		const revoke = `mutation($id: ID!) { revokeAccess(id: $id) }`
		friend.fails(t, "FORBIDDEN", revoke, map[string]any{"id": id})
		owner.must(t, revoke, map[string]any{"id": id}, nil)
		friend.isNull(t, "asset", w.c1.asset)
		owner.fails(t, "NOT_FOUND", revoke, map[string]any{"id": id})

		// Admin on one asset is no rights on the project or on other assets.
		owner.fails(t, "NOT_FOUND", grantAccess, map[string]any{"in": map[string]any{"scope": "PROJECT", "scopeId": w.projectA, "level": "VIEW", "userId": friend.id}})
		owner.fails(t, "NOT_FOUND", grantAccess, map[string]any{"in": map[string]any{"scope": "ASSET", "scopeId": w.c2.asset, "level": "VIEW", "userId": friend.id}})
		// The asset owner may delete it; the files stay.
		owner.must(t, `mutation($id: ID!) { deleteAsset(id: $id) }`, map[string]any{"id": w.c1.asset}, nil)
		w.admin.isNull(t, "asset", w.c1.asset)
		w.admin.sees(t, "file", w.c1.file)
		var grants int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM permission_grant WHERE scope_id = $1`, w.c1.asset).Scan(&grants); err != nil || grants != 0 {
			t.Fatalf("%d grants left on the deleted asset (%v)", grants, err)
		}
	})
}

// A file's level is the highest over the assets that use it, so attaching a file one can only
// view to an asset one can edit would reveal where it is stored.
func TestAttachingAFileNeedsDownloadOriginal(t *testing.T) {
	w := newWorld(t, 26)
	u, bystander := signIn(t), signIn(t)
	grant(t, w.admin, "ASSET", w.c1.asset, "VIEW", to(u))
	grant(t, w.admin, "ASSET", w.c2.asset, "EDIT", to(u))
	grant(t, w.admin, "ASSET", w.c2.asset, "DOWNLOAD_ORIGINAL", to(bystander))
	rendition := newRendition(t, u, w.c2.version)

	const attachFile = `mutation($r: ID!, $f: ID!) { attachFile(renditionId: $r, fileId: $f, role: VIDEO) { position } }`
	withSource := func(file string) map[string]any {
		return map[string]any{"in": map[string]any{"versionId": w.c2.version, "language": "sv", "format": "SRT", "content": w.f.SRT(1), "sourceFileId": file}}
	}
	copies := func(who user) int {
		t.Helper()
		var out struct {
			File *struct{ Copies []struct{ Path string } }
		}
		who.must(t, `query($id: ID!) { file(id: $id) { copies { path } } }`, map[string]any{"id": w.c1.file}, &out)
		if out.File == nil {
			return -1
		}
		return len(out.File.Copies)
	}

	u.fails(t, "FORBIDDEN", attachFile, map[string]any{"r": rendition, "f": w.c1.file})
	u.fails(t, "FORBIDDEN", importSubtitles, withSource(w.c1.file))
	u.fails(t, "INVALID_INPUT", attachFile, map[string]any{"r": rendition, "f": w.c3.file})
	if got := copies(u); got != 0 {
		t.Fatalf("viewer of the file sees %d copies after a refused attach", got)
	}
	if got := copies(bystander); got != -1 {
		t.Fatalf("file visible to a user of the other asset: %d copies", got)
	}

	grant(t, w.admin, "ASSET", w.c1.asset, "DOWNLOAD_ORIGINAL", to(u))
	attach(t, u, rendition, w.c1.file)
	u.must(t, importSubtitles, withSource(w.c1.file), nil)
	if got := copies(u); got != 1 {
		t.Fatalf("downloader sees %d copies, want 1", got)
	}
}

// The link carries the project's id, so it is hidden with the project: a null nested project
// would not be enough.
func TestAssetLinksToHiddenProjectsAreLeftOut(t *testing.T) {
	w := newWorld(t, 27)
	w.admin.must(t, `mutation($a: ID!, $p: ID!) { linkAssetToProject(assetId: $a, projectId: $p, role: REFERENCE) { role } }`,
		map[string]any{"a": w.c1.asset, "p": w.projectB}, nil)
	assetOnly, inB := signIn(t), signIn(t)
	grant(t, w.admin, "ASSET", w.c1.asset, "ADMIN", to(assetOnly))
	grant(t, w.admin, "ASSET", w.c1.asset, "VIEW", to(inB))
	grant(t, w.admin, "PROJECT", w.projectB, "VIEW", to(inB))

	links := func(u user) string {
		var out struct {
			Asset struct {
				Projects []struct {
					ProjectID string
					Role      string
					Project   *struct{ ID string }
				}
			}
		}
		u.must(t, `query($id: ID!) { asset(id: $id) { projects { projectId role createdAt project { id } } } }`, map[string]any{"id": w.c1.asset}, &out)
		var got []string
		for _, l := range out.Asset.Projects {
			if l.Project == nil || l.Project.ID != l.ProjectID {
				t.Errorf("link to %s without its project: %+v", l.ProjectID, l)
			}
			got = append(got, l.ProjectID+" "+l.Role)
		}
		sort.Strings(got)
		return strings.Join(got, ",")
	}
	both := []string{w.projectA + " SOURCE", w.projectB + " REFERENCE"}
	sort.Strings(both)
	for name, c := range map[string]struct {
		u    user
		want string
	}{
		"asset grant only, even ADMIN": {assetOnly, ""},
		"one of the projects":          {inB, w.projectB + " REFERENCE"},
		"administrator":                {w.admin, strings.Join(both, ",")},
	} {
		if got := links(c.u); got != c.want {
			t.Errorf("%s: links = %q, want %q", name, got, c.want)
		}
	}
	assetOnly.isNull(t, "project", w.projectA)
}

func TestGrantInheritanceGroupsAndLinks(t *testing.T) {
	w := newWorld(t, 23)

	t.Run("program grant reaches its projects and assets", func(t *testing.T) {
		u := signIn(t)
		id := grant(t, w.admin, "PROGRAM", w.prog.ID, "EDIT", to(u))
		if got := w.visible(t, u); got != "c1,c2,c3" {
			t.Fatalf("sees %q", got)
		}
		u.sees(t, "project", w.projectB)
		project := newProject(t, u, w.f, w.prog) // edit on the program allows new projects
		created := newAsset(t, u, w.f, project)
		u.sees(t, "asset", created)
		u.must(t, `mutation($a: ID!, $p: ID!) { linkAssetToProject(assetId: $a, projectId: $p, role: REFERENCE) { role } }`,
			map[string]any{"a": w.c1.asset, "p": w.projectB}, nil)
		u.must(t, `mutation($a: ID!, $p: ID!) { unlinkAssetFromProject(assetId: $a, projectId: $p, role: REFERENCE) }`,
			map[string]any{"a": w.c1.asset, "p": w.projectB}, nil)

		w.admin.must(t, `mutation($id: ID!) { revokeAccess(id: $id) }`, map[string]any{"id": id}, nil)
		if got := w.visible(t, u); got != "" {
			t.Fatalf("after revoke sees %q", got)
		}
		u.isNull(t, "asset", created)
		u.isNull(t, "program", w.prog.ID)
	})

	t.Run("group grant", func(t *testing.T) {
		group := "crew-" + unique()
		member, outsider := signIn(t, group), signIn(t)
		var groupID string
		if err := pool.QueryRow(context.Background(), `SELECT id FROM user_group WHERE external_id = $1`, group).Scan(&groupID); err != nil {
			t.Fatal(err)
		}
		grant(t, w.admin, "PROJECT", w.projectB, "VIEW", map[string]any{"groupId": groupID})
		if got := w.visible(t, member); got != "c3" {
			t.Fatalf("member sees %q", got)
		}
		if got := w.visible(t, outsider); got != "" {
			t.Fatalf("outsider sees %q", got)
		}
		// The highest of the user's own and their groups' grants counts.
		grant(t, w.admin, "ASSET", w.c3.asset, "EDIT", to(member))
		member.must(t, updateAsset, map[string]any{"id": w.c3.asset, "in": map[string]any{"typeId": 1, "title": w.marker + " " + w.c3.asset}}, nil)

		// Leaving the group at the identity provider ends the access with the next token.
		member.token = tokenFor(member.sub, member.email)
		member.isNull(t, "project", w.projectB)
		member.sees(t, "asset", w.c3.asset) // their own grant remains
	})

	t.Run("links decide which project grants apply", func(t *testing.T) {
		u := signIn(t)
		grant(t, w.admin, "PROJECT", w.projectB, "VIEW", to(u))
		u.isNull(t, "asset", w.c2.asset)
		link := map[string]any{"a": w.c2.asset, "p": w.projectB}
		w.admin.must(t, `mutation($a: ID!, $p: ID!) { linkAssetToProject(assetId: $a, projectId: $p, role: REFERENCE) { role } }`, link, nil)
		u.sees(t, "asset", w.c2.asset)
		w.admin.must(t, `mutation($a: ID!, $p: ID!) { unlinkAssetFromProject(assetId: $a, projectId: $p, role: REFERENCE) }`, link, nil)
		u.isNull(t, "asset", w.c2.asset)

		// Deleting the project removes the access it gave.
		u.sees(t, "asset", w.c3.asset)
		w.admin.must(t, `mutation($id: ID!) { deleteProject(id: $id) }`, map[string]any{"id": w.projectB}, nil)
		u.isNull(t, "asset", w.c3.asset)
		w.admin.sees(t, "asset", w.c3.asset)
	})
}

func TestGrantValidationAndExpiry(t *testing.T) {
	w := newWorld(t, 24)
	u := signIn(t)
	in := func(patch map[string]any) map[string]any {
		g := map[string]any{"scope": "ASSET", "scopeId": w.c1.asset, "level": "VIEW", "userId": u.id}
		for k, v := range patch {
			if v == nil {
				delete(g, k)
			} else {
				g[k] = v
			}
		}
		return map[string]any{"in": g}
	}
	for name, vars := range map[string]map[string]any{
		"no target":       in(map[string]any{"userId": nil}),
		"two targets":     in(map[string]any{"userEmail": u.email}),
		"unknown email":   in(map[string]any{"userId": nil, "userEmail": "never@example.test"}),
		"unknown user":    in(map[string]any{"userId": "US-00000000000000000000000000"}),
		"unknown group":   in(map[string]any{"userId": nil, "groupId": "UG-00000000000000000000000000"}),
		"already expired": in(map[string]any{"expiresAt": time.Now().Add(-time.Minute)}),
	} {
		t.Run(name, func(t *testing.T) { w.admin.fails(t, "INVALID_INPUT", grantAccess, vars) })
	}
	w.admin.fails(t, "NOT_FOUND", grantAccess, in(map[string]any{"scopeId": "AS-00000000000000000000000000"}))
	w.admin.fails(t, "NOT_FOUND", grantAccess, in(map[string]any{"scope": "PROJECT", "scopeId": "PJ-00000000000000000000000000"}))
	w.admin.fails(t, "NOT_FOUND", grantAccess, in(map[string]any{"scope": "PROGRAM", "scopeId": "PG-00000000000000000000000000"}))
	u.isNull(t, "asset", w.c1.asset)

	// Grants that expire: one on the asset, one on the project.
	soon := time.Now().Add(1500 * time.Millisecond)
	w.admin.must(t, grantAccess, in(map[string]any{"expiresAt": soon}), nil)
	w.admin.must(t, grantAccess, in(map[string]any{"scope": "PROJECT", "scopeId": w.projectB, "expiresAt": soon}), nil)
	u.sees(t, "asset", w.c1.asset)
	u.sees(t, "asset", w.c3.asset)
	u.sees(t, "project", w.projectB)

	// A second user keeps a lower level that does not expire next to a higher one that does.
	mixed := signIn(t)
	grant(t, w.admin, "ASSET", w.c1.asset, "VIEW", to(mixed))
	w.admin.must(t, grantAccess, map[string]any{"in": map[string]any{
		"scope": "PROGRAM", "scopeId": w.prog.ID, "level": "DOWNLOAD_ORIGINAL", "userId": mixed.id, "expiresAt": soon}}, nil)
	level := func() string {
		var out struct{ Asset struct{ MyLevel string } }
		mixed.must(t, `query($id: ID!) { asset(id: $id) { myLevel } }`, map[string]any{"id": w.c1.asset}, &out)
		return out.Asset.MyLevel
	}
	copies := func() int {
		var out struct {
			File struct{ Copies []struct{ Path string } }
		}
		mixed.must(t, `query($id: ID!) { file(id: $id) { copies { path } } }`, map[string]any{"id": w.c1.file}, &out)
		return len(out.File.Copies)
	}
	if got := level(); got != "DOWNLOAD_ORIGINAL" || copies() != 1 || w.visible(t, mixed) != "c1,c2,c3" {
		t.Fatalf("before expiry: level %s, %d copies, sees %s", got, copies(), w.visible(t, mixed))
	}

	// Expiry is the clock's doing: nothing sweeps between here and the checks below.
	time.Sleep(time.Until(soon) + 200*time.Millisecond)
	u.isNull(t, "project", w.projectB)
	u.isNull(t, "asset", w.c1.asset)
	u.isNull(t, "asset", w.c3.asset)
	u.isNull(t, "file", w.c1.file)
	if got := w.visible(t, u); got != "" {
		t.Fatalf("assets listed after the grants expired: %s", got)
	}
	if got := level(); got != "VIEW" || copies() != 0 || w.visible(t, mixed) != "c1" {
		t.Fatalf("after expiry: level %s, %d copies, sees %s; want the grant that did not expire", got, copies(), w.visible(t, mixed))
	}

	// The sweeper only tidies up: it deletes the expired grants and their rows.
	svc := access.NewService(sqlc.New(pool), db.PoolTx(pool))
	if n, err := svc.Sweep(context.Background()); err != nil || n < 3 {
		t.Fatalf("sweep removed %d grants: %v", n, err)
	}
	var stale int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM asset_access WHERE expires_at <= now()`).Scan(&stale); err != nil || stale != 0 {
		t.Fatalf("%d expired rows left after the sweep (%v)", stale, err)
	}
	if got := level(); got != "VIEW" {
		t.Fatalf("after the sweep: level %s", got)
	}
	if n, err := svc.Sweep(context.Background()); err != nil || n != 0 {
		t.Fatalf("second sweep removed %d: %v", n, err)
	}
}
