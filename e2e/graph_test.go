//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/asset"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/file"
	"github.com/bcc-media/mediabrain/internal/graph"
	"github.com/bcc-media/mediabrain/internal/project"
	"github.com/bcc-media/mediabrain/internal/subtitle"
	"github.com/bcc-media/mediabrain/internal/vidispineimport"
	"github.com/bcc-media/mediabrain/pkg/vidispine"
)

var errInjected = errors.New("injected database failure: SELECT secret FROM somewhere")

// probe counts the statements sent to the database and can make the n-th one fail.
type probe struct {
	calls  atomic.Int64
	failAt int64 // 0: never
}

func (p *probe) next() error {
	if n := p.calls.Add(1); p.failAt != 0 && n == p.failAt {
		return errInjected
	}
	return nil
}

type probedDB struct {
	inner sqlc.DBTX
	p     *probe
}

type failedRow struct{}

func (failedRow) Scan(...any) error { return errInjected }

func (d probedDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if err := d.p.next(); err != nil {
		return pgconn.CommandTag{}, err
	}
	return d.inner.Exec(ctx, sql, args...)
}

func (d probedDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if err := d.p.next(); err != nil {
		return nil, err
	}
	return d.inner.Query(ctx, sql, args...)
}

func (d probedDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if err := d.p.next(); err != nil {
		return failedRow{}
	}
	return d.inner.QueryRow(ctx, sql, args...)
}

func (d probedDB) CopyFrom(ctx context.Context, table pgx.Identifier, cols []string, src pgx.CopyFromSource) (int64, error) {
	if err := d.p.next(); err != nil {
		return 0, err
	}
	return d.inner.CopyFrom(ctx, table, cols, src)
}

type probedTx struct{ p *probe }

func (x probedTx) InTx(ctx context.Context, fn func(q sqlc.Querier) error) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return fn(sqlc.New(probedDB{tx, x.p})) })
}

// probedServices builds the domain services over a probed database connection.
func probedServices(p *probe) *graph.Resolver {
	q, tx := sqlc.New(probedDB{pool, p}), probedTx{p}
	src, _ := vidispine.New(vsServer.Config())
	return &graph.Resolver{
		VidispineSvc: vidispineimport.NewService(q, tx, src),
		AccessSvc:    access.NewService(q, tx), ProjectsSvc: project.NewService(q, tx), FilesSvc: file.NewService(q, tx),
		AssetsSvc: asset.NewService(q, tx), SubtitlesSvc: subtitle.NewService(q, tx),
	}
}

// exec runs one GraphQL operation in-process as the given principal and reports how many
// statements it sent to the database.
func exec(t *testing.T, as access.Principal, failAt int64, query string, vars map[string]any) (gqlResponse, int64) {
	t.Helper()
	p := &probe{failAt: failAt}
	srv := graph.NewServer(probedServices(p), false)
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req.WithContext(access.With(req.Context(), as)))
	var r gqlResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return r, p.calls.Load()
}

const deepQuery = `query($t: String) { assets(filter: {title: $t}, limit: 500) {
	id myLevel type { name }
	projects { role project { id program { code projects { id } } } }
	versions { number renditions { kind
		files { role file { id hashes { algo value } copies { path location { name } } } }
		subtitleTracks { revision cues { text } srt: export(format: SRT) }
		history: subtitleTracks(history: true) { revision cues(limit: 1) { text } } } }
} }`

// TestNestedQueriesAreBatched proves there is no N+1: the number of database statements for
// a deeply nested query does not grow with the number of assets.
func TestNestedQueriesAreBatched(t *testing.T) {
	w := newWorld(t, 30)
	viewer := signIn(t)
	grant(t, w.admin, "PROGRAM", w.prog.ID, "DOWNLOAD_ORIGINAL", to(viewer))
	principals := map[string]access.Principal{
		"admin":  {UserID: w.admin.id, Admin: true},
		"viewer": {UserID: viewer.id},
	}

	run := func(as access.Principal, wantAssets int) int64 {
		t.Helper()
		r, calls := exec(t, as, 0, deepQuery, map[string]any{"t": w.marker})
		if len(r.Errors) > 0 {
			t.Fatalf("errors: %+v", r.Errors)
		}
		var data struct {
			Assets []struct {
				Versions []struct {
					Renditions []struct {
						Files []struct {
							File struct{ Copies []struct{ Path string } }
						}
					}
				}
			}
		}
		if err := json.Unmarshal(r.Data, &data); err != nil || len(data.Assets) != wantAssets {
			t.Fatalf("%d assets, want %d (%v)", len(data.Assets), wantAssets, err)
		}
		for _, a := range data.Assets {
			if len(a.Versions) != 1 || len(a.Versions[0].Renditions) == 0 || len(a.Versions[0].Renditions[0].Files[0].File.Copies) != 1 {
				t.Fatalf("incomplete tree: %+v", a)
			}
		}
		return calls
	}

	before := map[string]int64{}
	for name, p := range principals {
		before[name] = run(p, 3)
	}
	for range 12 {
		c := newClip(t, w.admin, w.f, w.projectA, w.location)
		w.admin.must(t, updateAsset, map[string]any{"id": c.asset, "in": map[string]any{"typeId": 2, "title": w.marker + " more"}}, nil)
		w.admin.must(t, importSubtitles, map[string]any{"in": map[string]any{"versionId": c.version, "language": "nb", "format": "SRT", "content": w.f.SRT(4)}}, nil)
	}
	for name, p := range principals {
		after := run(p, 15)
		if after != before[name] {
			t.Errorf("%s: %d statements for 3 assets but %d for 15", name, before[name], after)
		}
		if after > 25 {
			t.Errorf("%s: %d statements for one query", name, after)
		}
	}
}

type operation struct {
	name  string
	query string
	// vars is called before every attempt, so it can prepare fresh objects.
	vars func() map[string]any
}

// TestDatabaseFailuresAreContained makes every database statement of every operation fail in
// turn. Each failure must surface as a generic INTERNAL error that tells the client nothing,
// and must leave nothing half-written: the same operation succeeds when retried.
func TestDatabaseFailuresAreContained(t *testing.T) {
	w := newWorld(t, 31)
	owner := signIn(t)
	grant(t, w.admin, "PROGRAM", w.prog.ID, "ADMIN", to(owner))
	other := signIn(t)
	fixed := func(v map[string]any) func() map[string]any { return func() map[string]any { return v } }
	id := func(v string) func() map[string]any { return fixed(map[string]any{"id": v}) }

	importTrack := func() string {
		var out struct{ ImportSubtitles trackJSON }
		w.admin.must(t, importSubtitles, map[string]any{"in": map[string]any{"versionId": w.c2.version, "language": "de", "format": "SRT", "content": w.f.SRT(2)}}, &out)
		return out.ImportSubtitles.ID
	}
	var sha string
	if err := pool.QueryRow(context.Background(), `SELECT encode(value, 'hex') FROM file_hash WHERE file_id = $1 AND algo = 'sha256'`, w.c1.file).Scan(&sha); err != nil {
		t.Fatal(err)
	}
	spare := newFile(t, w.admin, w.f, "")

	asOwner := []operation{
		{"deep query", deepQuery, fixed(map[string]any{"t": w.marker})},
		{"me", `{ me { id } }`, fixed(nil)},
		{"lists", `query($p: ID) { programs { id } projects(programId: $p, status: PLANNED) { id } assetTypes { id } languages { code } }`, fixed(map[string]any{"p": w.prog.ID})},
		{"program", `query($id: ID!) { program(id: $id) { id projects { id } } }`, id(w.prog.ID)},
		{"project", `query($id: ID!) { project(id: $id) { id program { id } } }`, id(w.projectA)},
		{"asset", `query($id: ID!) { asset(id: $id) { id myLevel } assetVersion(id: "` + w.c1.version + `") { id } rendition(id: "` + w.c1.rendition + `") { id } }`, id(w.c1.asset)},
		{"file", `query($id: ID!) { file(id: $id) { id hashes { algo } copies { id location { id } } } }`, id(w.c1.file)},
		{"file by hash", `query($v: String!) { fileByHash(hash: {algo: SHA256, value: $v}) { id } }`, fixed(map[string]any{"v": sha})},
		{"tracks", `query($id: ID!) { subtitleTracks(assetId: $id, history: true) { id } subtitleTrack(id: "` + w.track + `") { id cues { seq } export(format: VTT) } }`, id(w.c1.asset)},
		{"grants", `query($id: ID!) { a: grants(scope: PROGRAM, scopeId: $id) { id } b: grants(scope: PROJECT, scopeId: "` + w.projectA + `") { id } c: grants(scope: ASSET, scopeId: "` + w.c1.asset + `") { id } }`, id(w.prog.ID)},
		{"update program", `mutation($id: ID!) { updateProgram(id: $id, input: {name: "Renamed"}) { id } }`, id(w.prog.ID)},
		{"create project", createProject, func() map[string]any { return map[string]any{"in": projectVars(w.f, w.prog)} }},
		{"update project", updateProject, fixed(map[string]any{"id": w.projectA, "in": map[string]any{"name": "A"}})},
		{"delete project", `mutation($id: ID!) { deleteProject(id: $id) }`, func() map[string]any {
			p := newProject(t, w.admin, w.f, w.prog)
			newAsset(t, w.admin, w.f, p)
			return map[string]any{"id": p}
		}},
		{"create asset", createAsset, func() map[string]any { return map[string]any{"p": w.projectA, "in": assetVars(w.f)} }},
		{"update asset", updateAsset, fixed(map[string]any{"id": w.c2.asset, "in": map[string]any{"typeId": 1, "title": w.marker + " c2"}})},
		{"delete asset", `mutation($id: ID!) { deleteAsset(id: $id) }`, func() map[string]any { return map[string]any{"id": newAsset(t, w.admin, w.f, w.projectA)} }},
		{"link", `mutation($a: ID!, $p: ID!) { linkAssetToProject(assetId: $a, projectId: $p, role: DELIVERABLE) { role } }`,
			func() map[string]any {
				return map[string]any{"a": newAsset(t, w.admin, w.f, w.projectA), "p": w.projectB}
			}},
		{"unlink", `mutation($a: ID!, $p: ID!) { unlinkAssetFromProject(assetId: $a, projectId: $p, role: SOURCE) }`,
			func() map[string]any {
				return map[string]any{"a": newAsset(t, w.admin, w.f, w.projectB), "p": w.projectB}
			}},
		{"create version", `mutation($id: ID!) { createAssetVersion(assetId: $id, input: {}) { id } }`, id(w.c2.asset)},
		{"update version", `mutation($id: ID!) { updateAssetVersion(id: $id, input: {status: REVIEW}) { id } }`, id(w.c2.version)},
		{"delete version", `mutation($id: ID!) { deleteAssetVersion(id: $id) }`, func() map[string]any { return map[string]any{"id": newVersion(t, w.admin, w.c2.asset)} }},
		{"create rendition", `mutation($id: ID!) { createRendition(versionId: $id, input: {kind: ENCODE}) { id } }`, id(w.c2.version)},
		{"update rendition", `mutation($id: ID!) { updateRendition(id: $id, input: {purpose: "web"}) { id } }`, id(w.c2.rendition)},
		{"delete rendition", `mutation($id: ID!) { deleteRendition(id: $id) }`, func() map[string]any { return map[string]any{"id": newRendition(t, w.admin, w.c2.version)} }},
		{"attach file", `mutation($r: ID!, $f: ID!) { attachFile(renditionId: $r, fileId: $f, role: SIDECAR) { position } }`,
			func() map[string]any {
				return map[string]any{"r": newRendition(t, w.admin, w.c2.version), "f": w.c1.file}
			}},
		{"detach file", `mutation($r: ID!, $f: ID!) { detachFile(renditionId: $r, fileId: $f, role: VIDEO) }`, func() map[string]any {
			r := newRendition(t, w.admin, w.c2.version)
			attach(t, w.admin, r, w.c1.file)
			return map[string]any{"r": r, "f": w.c1.file}
		}},
		{"import subtitles", importSubtitles, func() map[string]any {
			return map[string]any{"in": map[string]any{"versionId": w.c3.version, "language": "fr", "format": "VTT", "content": w.f.VTT(3), "sourceFileId": w.c3.file}}
		}},
		{"edit subtitles", editSubtitles, func() map[string]any {
			return map[string]any{"id": importTrack(), "cues": []map[string]any{{"startMs": 0, "endMs": 5, "text": "x"}}}
		}},
		{"delete track", `mutation($id: ID!) { deleteSubtitleTrack(id: $id) }`, func() map[string]any { return map[string]any{"id": importTrack()} }},
		{"grant by id", grantAccess, fixed(map[string]any{"in": map[string]any{"scope": "ASSET", "scopeId": w.c2.asset, "level": "VIEW", "userId": other.id}})},
		{"grant by email", grantAccess, fixed(map[string]any{"in": map[string]any{"scope": "PROJECT", "scopeId": w.projectA, "level": "VIEW", "userEmail": other.email}})},
		{"grant on program", grantAccess, fixed(map[string]any{"in": map[string]any{"scope": "PROGRAM", "scopeId": w.prog.ID, "level": "VIEW", "userId": other.id}})},
		{"revoke", `mutation($id: ID!) { revokeAccess(id: $id) }`, func() map[string]any {
			return map[string]any{"id": grant(t, w.admin, "PROJECT", w.projectB, "VIEW", to(other))}
		}},
	}
	location := func() map[string]any {
		in := w.f.Location()
		return map[string]any{"name": in.Name, "kind": "NAS", "baseUri": in.BaseURI}
	}
	copyOf := func(fileID string) string {
		var out struct{ AddFileCopy struct{ ID string } }
		w.admin.must(t, `mutation($f: ID!, $c: FileCopyInput!) { addFileCopy(fileId: $f, copy: $c) { id } }`,
			map[string]any{"f": fileID, "c": map[string]any{"locationId": w.location, "path": w.f.Path("mov")}}, &out)
		return out.AddFileCopy.ID
	}
	known := w.f.VideoFile()
	w.admin.must(t, registerFile, map[string]any{"in": fileVars(known)}, nil)
	var imp struct{ ImportFromVidispine struct{ ID string } }
	w.admin.must(t, importFromVidispine, map[string]any{"in": map[string]any{"itemId": vidispineItem(w.f, newVidispineStorage()).ID, "typeId": 1}}, &imp)
	imported := imp.ImportFromVidispine.ID
	machine := signInService(t, false)
	asAdmin := []operation{
		{"admin lists", `{ users { id } userGroups { id } files { id } storageLocations { id } }`, fixed(nil)},
		{"create program", createProgram, func() map[string]any {
			in := w.f.Program()
			return map[string]any{"in": map[string]any{"code": in.Code, "name": in.Name}}
		}},
		{"delete program", `mutation($id: ID!) { deleteProgram(id: $id) }`, func() map[string]any { return map[string]any{"id": newProgram(t, w.admin, w.f).ID} }},
		{"create location", `mutation($in: StorageLocationInput!) { createStorageLocation(input: $in) { id } }`, func() map[string]any { return map[string]any{"in": location()} }},
		{"update location", `mutation($id: ID!, $in: StorageLocationInput!) { updateStorageLocation(id: $id, input: $in) { id } }`,
			func() map[string]any { return map[string]any{"id": w.location, "in": location()} }},
		{"delete location", `mutation($id: ID!) { deleteStorageLocation(id: $id) }`, func() map[string]any { return map[string]any{"id": newLocation(t, w.admin, w.f)} }},
		{"register new file", registerFile, func() map[string]any {
			return map[string]any{"in": fileVars(w.f.VideoFile(), map[string]any{"locationId": w.location, "path": w.f.Path("mxf")})}
		}},
		{"register known file", registerFile, func() map[string]any {
			again := known
			again.Hashes = append([]fakeHash{known.Hashes[0]}, w.f.Hashes()[1])
			again.Hashes[1].Algo = sqlc.HashAlgoBlake3
			return map[string]any{"in": fileVars(again, map[string]any{"locationId": w.location, "path": w.f.Path("mov")})}
		}},
		{"add copy", `mutation($f: ID!, $c: FileCopyInput!) { addFileCopy(fileId: $f, copy: $c) { id } }`,
			func() map[string]any {
				return map[string]any{"f": spare, "c": map[string]any{"locationId": w.location, "path": w.f.Path("mov")}}
			}},
		{"copy status", `mutation($id: ID!) { setFileCopyStatus(id: $id, status: MISMATCH) { id } }`, func() map[string]any { return map[string]any{"id": copyOf(spare)} }},
		{"remove copy", `mutation($id: ID!) { removeFileCopy(id: $id) }`, func() map[string]any { return map[string]any{"id": copyOf(spare)} }},
		{"delete file", `mutation($id: ID!) { deleteFile(id: $id) }`, func() map[string]any { return map[string]any{"id": newFile(t, w.admin, w.f, "")} }},
		{"set admin", `mutation($u: String!) { setUserAdmin(user: $u, admin: false) { id } }`, fixed(map[string]any{"u": other.email})},
		{"create asset without project", createAsset, func() map[string]any { return map[string]any{"in": assetVars(w.f)} }},
		{"import from vidispine", importFromVidispine, func() map[string]any {
			// A new storage every time, so that creating its location is swept as well.
			item := vidispineItem(w.f, newVidispineStorage())
			return map[string]any{"in": map[string]any{"itemId": item.ID, "typeId": 1, "projectId": w.projectA}}
		}},
		{"refresh from vidispine", refreshFromVidispine, id(imported)},
		{"service lists", `query($s: String!) { serviceAccounts { id } serviceTokens(serviceAccount: $s) { id } }`, fixed(map[string]any{"s": machine.sub})},
		{"create service account", createServiceAccount, func() map[string]any { return map[string]any{"in": map[string]any{"name": "sweep-" + unique()}} }},
		{"service account active", setServiceActive, fixed(map[string]any{"s": machine.sub, "a": true})},
		{"create service token", createServiceToken, fixed(map[string]any{"in": map[string]any{"serviceAccount": machine.id, "name": "sweep"}})},
		{"revoke service token", revokeServiceToken, func() map[string]any {
			return map[string]any{"id": newServiceToken(t, w.admin, machine.sub).ServiceToken.ID}
		}},
	}

	sweep := func(as access.Principal, ops []operation) {
		for _, op := range ops {
			t.Run(op.name, func(t *testing.T) {
				for failAt := int64(1); ; failAt++ {
					if failAt > 100 {
						t.Fatal("operation never succeeded")
					}
					r, calls := exec(t, as, failAt, op.query, op.vars())
					if calls < failAt {
						if len(r.Errors) > 0 {
							t.Fatalf("failed without an injected fault: %+v", r.Errors)
						}
						return // every statement was allowed to run
					}
					if len(r.Errors) == 0 {
						t.Fatalf("statement %d failed but the response has no error", failAt)
					}
					for _, e := range r.Errors {
						if e.Extensions.Code != "INTERNAL" || e.Message != "internal error" {
							t.Fatalf("statement %d: error leaked as %+v", failAt, e)
						}
					}
					if strings.Contains(string(r.Data), "secret") {
						t.Fatalf("statement %d: cause leaked into data", failAt)
					}
				}
			})
		}
	}
	sweep(access.Principal{UserID: owner.id}, asOwner)
	sweep(access.Principal{UserID: w.admin.id, Admin: true}, asAdmin)

	// The services used outside GraphQL get the same treatment.
	direct := map[string]func(svc *access.Service) error{
		"sign in": func(svc *access.Service) error {
			_, err := svc.SignIn(context.Background(), access.Identity{Subject: other.sub, Email: other.email, Name: "N", Groups: []string{"fault-crew"}})
			return err
		},
		"debug admin": func(svc *access.Service) error {
			_, err := svc.DebugAdmin(context.Background(), "fault-"+unique()+"@example.test")
			return err
		},
		"impersonate": func(svc *access.Service) error {
			_, err := svc.Impersonate(context.Background(), other.email)
			return err
		},
		"sweep": func(svc *access.Service) error { _, err := svc.Sweep(context.Background()); return err },
		"service sign in": func(svc *access.Service) error {
			_, _, err := svc.ServiceSignIn(context.Background(), machine.token)
			return err
		},
		"create group": func(svc *access.Service) error {
			_, err := svc.CreateGroup(access.With(context.Background(), access.System()), "group "+unique(), []string{other.id})
			return err
		},
	}
	for name, call := range direct {
		t.Run(name, func(t *testing.T) {
			for failAt := int64(1); failAt <= 100; failAt++ {
				p := &probe{failAt: failAt}
				err := call(probedServices(p).AccessSvc)
				if p.calls.Load() < failAt {
					if err != nil {
						t.Fatalf("failed without an injected fault: %v", err)
					}
					return
				}
				if !errors.Is(err, errInjected) {
					t.Fatalf("statement %d: err = %v", failAt, err)
				}
			}
			t.Fatal("never succeeded")
		})
	}
}

// TestAuditLog checks that changes and refusals are logged with who did what to which object.
func TestAuditLog(t *testing.T) {
	w := newWorld(t, 32)
	viewer := signIn(t)
	grant(t, w.admin, "ASSET", w.c1.asset, "VIEW", to(viewer))

	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(old)

	vars := map[string]any{"id": w.c1.asset, "in": map[string]any{"typeId": 1, "title": "x"}}
	if r, _ := exec(t, access.Principal{UserID: viewer.id}, 0, updateAsset, vars); len(r.Errors) != 1 {
		t.Fatalf("expected a refusal, got %+v", r.Errors)
	}
	if r, _ := exec(t, access.Principal{UserID: viewer.id}, 0, `{ me { id } }`, nil); len(r.Errors) != 0 {
		t.Fatalf("me: %+v", r.Errors)
	}
	type entry struct{ Msg, User, Mutation, ID, Path string }
	var entries []entry
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var e entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		entries = append(entries, e)
	}
	want := []entry{
		{Msg: "audit: mutation", User: viewer.id, Mutation: "updateAsset", ID: w.c1.asset},
		{Msg: "audit: denied", User: viewer.id, Path: "updateAsset"},
	}
	if len(entries) != 2 || entries[0] != want[0] || entries[1] != want[1] {
		t.Fatalf("audit log = %+v, want %+v (queries are not logged)", entries, want)
	}
}
