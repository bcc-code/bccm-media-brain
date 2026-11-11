//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/app"
	"github.com/bcc-media/mediabrain/internal/cli"
	"github.com/bcc-media/mediabrain/internal/config"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/devdata"
	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/internal/fake"
	"github.com/bcc-media/mediabrain/internal/vidispineimport"
	"github.com/bcc-media/mediabrain/pkg/vidispine"
	"github.com/bcc-media/mediabrain/pkg/vidispine/vidispinetest"
)

const (
	importedFields = `id title description legacyIds metadata
		versions { label renditions { kind metadata files { role file {
			sizeBytes mimeType durationMs codec width height hashes { algo value }
			copies { path location { name kind baseUri online } } } } } }`
	importFromVidispine  = `mutation($in: VidispineImportInput!) { importFromVidispine(input: $in) { ` + importedFields + ` } }`
	refreshFromVidispine = `mutation($id: ID!) { refreshFromVidispine(assetId: $id) { ` + importedFields + ` } }`
)

type importedAsset struct {
	ID          string
	Title       string
	Description *string
	LegacyIds   []string
	Metadata    map[string]json.RawMessage
	Versions    []struct {
		Label      string
		Renditions []struct {
			Kind     string
			Metadata map[string]map[string]string
			Files    []struct {
				Role string
				File struct {
					SizeBytes, DurationMs int64
					MimeType, Codec       string
					Width, Height         int
					Hashes                []struct{ Algo, Value string }
					Copies                []struct {
						Path     string
						Location struct {
							Name, Kind, BaseUri string
							Online              bool
						}
					}
				}
			}
		}
	}
}

func (a importedAsset) vidispine(t *testing.T) vidispineimport.Metadata {
	t.Helper()
	var m vidispineimport.Metadata
	if err := json.Unmarshal(a.Metadata[vidispineimport.MetadataKey], &m); err != nil {
		t.Fatalf("metadata.vidispine of %s: %v", a.ID, err)
	}
	return m
}

var vidispineIDs atomic.Int64

func vidispineID() string { return fmt.Sprintf("VX-%d", 50_000_000+vidispineIDs.Add(1)) }

// newVidispineStorage adds a storage to the fake Vidispine that is reachable through S3 with
// credentials and through a local path.
func newVidispineStorage() string {
	id := vidispineID()
	vsServer.AddStorage(id, "s3://ACCESSKEY:secretkey@bucket-"+id+"/", "file:///mnt/"+id+"/")
	return id
}

// vidispineItem adds a made-up item with an original file on the storage to the fake Vidispine.
func vidispineItem(f *fake.Faker, storageID string) fake.VidispineItem {
	item := f.VidispineItem(storageID)
	item.ID = vidispineID()
	vsServer.AddItem(item.ID, item.Fields, item.Shapes...)
	return item
}

func importVars(itemID string, projectID string) map[string]any {
	in := map[string]any{"itemId": itemID, "typeId": 1}
	if projectID != "" {
		in["projectId"] = projectID
	}
	return map[string]any{"in": in}
}

func importItem(t *testing.T, u user, itemID, projectID string) importedAsset {
	t.Helper()
	var out struct{ ImportFromVidispine importedAsset }
	u.must(t, importFromVidispine, importVars(itemID, projectID), &out)
	return out.ImportFromVidispine
}

func count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestVidispineImport(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(71)
	prog := newProgram(t, admin, f)
	project := newProject(t, admin, f, prog)
	viewer := signIn(t)
	grant(t, admin, "PROJECT", project, "DOWNLOAD_ORIGINAL", to(viewer))
	storage := newVidispineStorage()

	item := vidispineItem(f, storage)
	vsServer.AddTimedValue(item.ID, "title", "A chapter", "100@PAL", "200@PAL")
	vsServer.SetField(item.ID, "tags", "one", "", "two")
	a := importItem(t, admin, item.ID, project)

	orig := item.Shapes[0].ContainerComponent.File[0]
	if a.Title != item.Fields["title"] || a.Description != nil || len(a.LegacyIds) != 1 || a.LegacyIds[0] != item.ID {
		t.Fatalf("asset = %+v", a)
	}
	meta := a.vidispine(t)
	if meta.ItemID != item.ID || meta.RefreshedAt.IsZero() || len(meta.Fields["title"]) != 1 ||
		strings.Join(meta.Fields["tags"], ",") != "one,two" || meta.Fields["portal_mf982016"][0] != item.Fields["portal_mf982016"] {
		t.Fatalf("metadata.vidispine = %+v", meta)
	}
	if len(a.Versions) != 1 || a.Versions[0].Label != "Vidispine original" || len(a.Versions[0].Renditions) != 1 {
		t.Fatalf("versions = %+v", a.Versions)
	}
	r := a.Versions[0].Renditions[0]
	if r.Kind != "MASTER" || r.Metadata["vidispine"]["shapeId"] != item.Shapes[0].ID || r.Metadata["vidispine"]["fileId"] != orig.ID || len(r.Files) != 1 {
		t.Fatalf("rendition = %+v", r)
	}
	file := r.Files[0].File
	video := item.Shapes[0].VideoComponent[0]
	seconds, _ := item.Shapes[0].ContainerComponent.Duration.Seconds()
	if r.Files[0].Role != "VIDEO" || file.SizeBytes != orig.Size || file.MimeType != "application/mxf" || file.Codec != "mpeg2video" ||
		file.Width != video.Resolution.Width || file.Height != video.Resolution.Height || file.DurationMs != int64(seconds*1000) {
		t.Fatalf("file = %+v", file)
	}
	if len(file.Hashes) != 1 || file.Hashes[0].Algo != "SHA1" || file.Hashes[0].Value != orig.Hash {
		t.Fatalf("hashes = %+v", file.Hashes)
	}
	if len(file.Copies) != 1 || file.Copies[0].Path != orig.Path {
		t.Fatalf("copies = %+v", file.Copies)
	}
	// The location is the Vidispine storage: its local path, and never its credentials.
	loc := file.Copies[0].Location
	if loc.Name != storage || loc.Kind != "NAS" || loc.BaseUri != "file:///mnt/"+storage+"/" || !loc.Online {
		t.Fatalf("location = %+v", loc)
	}

	t.Run("project members see it", func(t *testing.T) {
		viewer.sees(t, "asset", a.ID)
		byLegacy := `query($id: String!) { assets(filter: {legacyId: $id}) { id } }`
		var out struct{ Assets []struct{ ID string } }
		viewer.must(t, byLegacy, map[string]any{"id": item.ID}, &out)
		if len(out.Assets) != 1 || out.Assets[0].ID != a.ID {
			t.Fatalf("by legacy id = %+v", out.Assets)
		}
	})

	t.Run("an item is imported once", func(t *testing.T) {
		e := admin.fails(t, "CONFLICT", importFromVidispine, importVars(item.ID, ""))
		if !strings.Contains(e.Message, a.ID) {
			t.Fatalf("message = %q", e.Message)
		}
		if n := count(t, `SELECT count(*) FROM asset WHERE legacy_ids @> ARRAY[$1]`, item.ID); n != 1 {
			t.Fatalf("assets for the item = %d", n)
		}
	})

	t.Run("the storage location is reused", func(t *testing.T) {
		second := importItem(t, admin, vidispineItem(f, storage).ID, "")
		if got := second.Versions[0].Renditions[0].Files[0].File.Copies[0].Location.Name; got != storage {
			t.Fatalf("location = %q", got)
		}
		if n := count(t, `SELECT count(*) FROM storage_location WHERE name = $1`, storage); n != 1 {
			t.Fatalf("locations named %s = %d", storage, n)
		}
		// Without a project only administrators see the asset.
		viewer.isNull(t, "asset", second.ID)
	})

	t.Run("the same content under two items is one file", func(t *testing.T) {
		twin := f.VidispineItem(storage)
		twin.ID = vidispineID()
		twin.Shapes[0].ContainerComponent.File[0].Hash = orig.Hash
		twin.Shapes[0].ContainerComponent.File[0].Size = orig.Size
		vsServer.AddItem(twin.ID, twin.Fields, twin.Shapes...)
		b := importItem(t, admin, twin.ID, "")
		if copies := b.Versions[0].Renditions[0].Files[0].File.Copies; len(copies) != 2 {
			t.Fatalf("copies = %+v", copies)
		}
	})

	t.Run("title falls back to the file name, then the id", func(t *testing.T) {
		named := f.VidispineItem(storage)
		named.ID = vidispineID()
		named.Fields["title"] = "  "
		vsServer.AddItem(named.ID, named.Fields, named.Shapes...)
		if got := importItem(t, admin, named.ID, ""); got.Title != named.Fields["originalFilename"] {
			t.Fatalf("title = %q", got.Title)
		}
		bare := vidispineID()
		vsServer.AddItem(bare, nil)
		if got := importItem(t, admin, " "+bare+" ", ""); got.Title != bare || len(got.Versions) != 0 {
			t.Fatalf("bare item = %+v", got)
		}
	})

	t.Run("placeholders have no files", func(t *testing.T) {
		p := f.VidispinePlaceholder()
		p.ID = vidispineID()
		vsServer.AddItem(p.ID, p.Fields, vidispine.Shape{ID: "VX-1", Tag: []string{vidispine.ShapeTagOriginal}})
		if got := importItem(t, admin, p.ID, project); got.Title != p.Fields["title"] || len(got.Versions) != 0 {
			t.Fatalf("placeholder = %+v", got)
		}
	})

	t.Run("audio and other files", func(t *testing.T) {
		s3 := vidispineID()
		vsServer.AddStorage(s3, "not a uri at all", "s3://KEY:secret@bucket/prefix/")
		audio := f.VidispineItem(s3)
		audio.ID = vidispineID()
		audio.Shapes[0].VideoComponent = nil
		audio.Shapes[0].MimeType = nil
		audio.Shapes[0].ContainerComponent.Duration = vidispine.Timestamp{}
		audio.Shapes[0].ContainerComponent.File[0].Path = "/audio/a.wav"
		vsServer.AddItem(audio.ID, audio.Fields, audio.Shapes...)
		got := importItem(t, admin, audio.ID, "").Versions[0].Renditions[0].Files[0]
		if got.Role != "AUDIO" || got.File.Codec != "pcm_s24le" || got.File.Width != 0 || got.File.DurationMs != 0 || got.File.MimeType != "" {
			t.Fatalf("audio file = %+v", got)
		}
		if c := got.File.Copies[0]; c.Path != "audio/a.wav" || c.Location.Kind != "S3" || c.Location.BaseUri != "s3://bucket/prefix/" {
			t.Fatalf("copy = %+v", c)
		}

		doc := f.VidispineItem(s3)
		doc.ID = vidispineID()
		binary := vidispine.Shape{ID: "VX-2", Tag: []string{vidispine.ShapeTagOriginal}, BinaryComponent: []vidispine.BinaryComponent{{
			File: doc.Shapes[0].ContainerComponent.File,
		}}}
		vsServer.AddItem(doc.ID, doc.Fields, binary)
		if got := importItem(t, admin, doc.ID, "").Versions[0].Renditions[0].Files[0]; got.Role != "SIDECAR" || got.File.Codec != "" {
			t.Fatalf("binary file = %+v", got)
		}
	})

	t.Run("refused items leave nothing behind", func(t *testing.T) {
		broken := func(change func(*vidispine.File)) string {
			it := f.VidispineItem(storage)
			it.ID = vidispineID()
			change(&it.Shapes[0].ContainerComponent.File[0])
			vsServer.AddItem(it.ID, it.Fields, it.Shapes...)
			return it.ID
		}
		noAddress := vidispineID()
		vsServer.AddStorage(noAddress)
		cases := map[string]string{
			"still open":       broken(func(f *vidispine.File) { f.State = "OPEN" }),
			"no hash":          broken(func(f *vidispine.File) { f.Hash = "" }),
			"md5 hash":         broken(func(f *vidispine.File) { f.Hash = "d41d8cd98f00b204e9800998ecf8427e" }),
			"no storage":       broken(func(f *vidispine.File) { f.Storage = "" }),
			"no path":          broken(func(f *vidispine.File) { f.Path = "/" }),
			"path escapes":     broken(func(f *vidispine.File) { f.Path = "a/../../etc/passwd" }),
			"storage unusable": broken(func(f *vidispine.File) { f.Storage = noAddress }),
		}
		for name, id := range cases {
			admin.fails(t, "INVALID_INPUT", importFromVidispine, importVars(id, project))
			if n := count(t, `SELECT count(*) FROM asset WHERE legacy_ids @> ARRAY[$1]`, id); n != 0 {
				t.Errorf("%s: %d assets left behind", name, n)
			}
		}
		for _, id := range []string{"", "123", "VX-", "VX-1; DROP TABLE asset", "vx-12"} {
			admin.fails(t, "INVALID_INPUT", importFromVidispine, importVars(id, ""))
		}
		unknownType := vidispineItem(f, storage).ID
		admin.fails(t, "INVALID_INPUT", importFromVidispine, map[string]any{"in": map[string]any{"itemId": unknownType, "typeId": 999}})
		admin.fails(t, "NOT_FOUND", importFromVidispine, importVars(vidispineItem(f, storage).ID, "PJ-00000000000000000000000000"))
		// A copy path that already holds different content.
		clash := broken(func(f *vidispine.File) { f.Path = orig.Path })
		admin.fails(t, "CONFLICT", importFromVidispine, importVars(clash, ""))
		if n := count(t, `SELECT count(*) FROM asset WHERE legacy_ids && $1`, []string{unknownType, clash}); n != 0 {
			t.Fatalf("%d assets left behind", n)
		}
	})

	t.Run("Vidispine problems", func(t *testing.T) {
		e := admin.fails(t, "NOT_FOUND", importFromVidispine, importVars(vidispineID(), ""))
		if !strings.Contains(e.Message, "Vidispine has no item") {
			t.Fatalf("message = %q", e.Message)
		}
		gone := f.VidispineItem(vidispineID()) // its storage does not exist
		gone.ID = vidispineID()
		vsServer.AddItem(gone.ID, gone.Fields, gone.Shapes...)
		admin.fails(t, "UNAVAILABLE", importFromVidispine, importVars(gone.ID, ""))

		waiting := vidispineItem(f, storage)
		vsServer.Fail(http.StatusInternalServerError)
		e = admin.fails(t, "UNAVAILABLE", importFromVidispine, importVars(waiting.ID, ""))
		vsServer.Fail(0)
		// The message says nothing about where Vidispine is or what it answered.
		if strings.Contains(e.Message, "127.0.0.1") || strings.Contains(e.Message, "induced") {
			t.Fatalf("message leaks the cause: %q", e.Message)
		}
		importItem(t, admin, waiting.ID, "")
	})
}

func TestVidispineRefresh(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(72)
	storage := newVidispineStorage()
	item := vidispineItem(f, storage)
	a := importItem(t, admin, item.ID, "")

	// The asset is edited in MediaBrain, the item in Vidispine.
	admin.must(t, updateAsset, map[string]any{"id": a.ID, "in": map[string]any{
		"typeId": 2, "title": "Edited here", "description": "ours", "legacyIds": a.LegacyIds,
		"metadata": map[string]any{"note": "kept", "vidispine": json.RawMessage(a.Metadata["vidispine"])},
	}}, nil)
	vsServer.SetField(item.ID, "title", "Changed in Vidispine")
	vsServer.SetField(item.ID, "portal_new", "added")
	vsServer.SetField(item.ID, "mediaType")

	var out struct{ RefreshFromVidispine importedAsset }
	admin.must(t, refreshFromVidispine, map[string]any{"id": a.ID}, &out)
	got := out.RefreshFromVidispine
	if got.Title != "Edited here" || got.Description == nil || *got.Description != "ours" || string(got.Metadata["note"]) != `"kept"` {
		t.Fatalf("refresh changed more than metadata.vidispine: %+v", got)
	}
	meta := got.vidispine(t)
	if meta.Fields["title"][0] != "Changed in Vidispine" || meta.Fields["portal_new"][0] != "added" || meta.Fields["mediaType"] != nil || meta.ItemID != item.ID {
		t.Fatalf("metadata.vidispine = %+v", meta)
	}
	if len(got.Versions) != 1 || len(got.Versions[0].Renditions[0].Files) != 1 {
		t.Fatalf("refresh touched the files: %+v", got.Versions)
	}

	t.Run("metadata wiped in MediaBrain", func(t *testing.T) {
		admin.must(t, updateAsset, map[string]any{"id": a.ID, "in": map[string]any{"typeId": 2, "title": "Edited here", "legacyIds": []string{"LEG-1", item.ID}}}, nil)
		admin.must(t, refreshFromVidispine, map[string]any{"id": a.ID}, &out)
		if out.RefreshFromVidispine.vidispine(t).ItemID != item.ID {
			t.Fatal("the legacy id did not identify the item")
		}
	})

	t.Run("refusals", func(t *testing.T) {
		plain := newAsset(t, admin, f, "")
		e := admin.fails(t, "INVALID_INPUT", refreshFromVidispine, map[string]any{"id": plain})
		if !strings.Contains(e.Message, "not imported from Vidispine") {
			t.Fatalf("message = %q", e.Message)
		}
		admin.fails(t, "NOT_FOUND", refreshFromVidispine, map[string]any{"id": "AS-00000000000000000000000000"})

		vsServer.Fail(http.StatusServiceUnavailable)
		admin.fails(t, "UNAVAILABLE", refreshFromVidispine, map[string]any{"id": a.ID})
		vsServer.Fail(0)

		vsServer.RemoveItem(item.ID)
		admin.fails(t, "NOT_FOUND", refreshFromVidispine, map[string]any{"id": a.ID})
		admin.sees(t, "asset", a.ID)
	})
}

func TestVidispineNotConfigured(t *testing.T) {
	ctx := context.Background()
	svc := vidispineimport.NewService(sqlc.New(pool), db.PoolTx(pool), nil)
	in := vidispineimport.ImportInput{ItemID: "VX-1", TypeID: 1}

	system := access.With(ctx, access.System())
	if _, err := svc.Import(system, in); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("import: %v", err)
	}
	if _, err := svc.Refresh(system, "AS-00000000000000000000000000"); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("refresh: %v", err)
	}
	// Who is asking is checked before anything else.
	if _, err := svc.Import(access.With(ctx, access.Principal{UserID: "US-1"}), in); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("import as a user: %v", err)
	}
	if _, err := svc.Refresh(ctx, "AS-1"); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("refresh as nobody: %v", err)
	}

	// The debug UI says so instead of offering the import.
	store := devdata.NewStore(pool)
	if store.VidispineConfigured() {
		t.Fatal("debug store claims Vidispine is configured")
	}
	if _, err := store.VidispineImport(ctx, in); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("debug import: %v", err)
	}

	cfg := appConfig()
	cfg.VidispineURL = "vidispine.example.com/API"
	if _, err := app.New(ctx, cfg); err == nil || !strings.Contains(err.Error(), "VIDISPINE_URL") {
		t.Fatalf("relative VIDISPINE_URL: %v", err)
	}
}

func TestVidispineCLI(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(73)
	project := newProject(t, admin, f, newProgram(t, admin, f))
	item := vidispineItem(f, newVidispineStorage())

	t.Setenv(config.EnvFileVar, filepath.Join(t.TempDir(), "missing.env"))
	t.Setenv("DATABASE_URL", dbURL)
	t.Setenv("VIDISPINE_URL", vsServer.URL)
	t.Setenv("VIDISPINE_USERNAME", vidispinetest.Username)
	t.Setenv("VIDISPINE_PASSWORD", vidispinetest.Password)
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := cli.Run(context.Background(), args, &out)
		return out.String(), err
	}

	out, err := run("vidispine", "import", "-type", "2", "-project", project, item.ID)
	if err != nil || !strings.Contains(out, "imported "+item.ID) || !strings.Contains(out, item.Fields["title"]) {
		t.Fatalf("import: %q %v", out, err)
	}
	assetID, _, _ := strings.Cut(out, ":")
	if n := count(t, `SELECT count(*) FROM asset a JOIN asset_project ap ON ap.asset_id = a.id WHERE a.id = $1 AND a.type_id = 2 AND ap.project_id = $2`, assetID, project); n != 1 {
		t.Fatalf("asset %q is not in the project with type 2", assetID)
	}
	if out, err := run("vidispine", "refresh", assetID); err != nil || out != assetID+": refreshed from "+item.ID+"\n" {
		t.Fatalf("refresh: %q %v", out, err)
	}

	other := vidispineItem(f, newVidispineStorage())
	if out, err := run("vidispine", "import", other.ID); err != nil || !strings.Contains(out, "imported") {
		t.Fatalf("import with defaults: %q %v", out, err)
	}
	if _, err := run("vidispine", "import", item.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second import: %v", err)
	}
	if _, err := run("vidispine", "refresh", "AS-00000000000000000000000000"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("refresh of nothing: %v", err)
	}

	t.Setenv("VIDISPINE_URL", "nowhere")
	if _, err := run("vidispine", "import", vidispineID()); err == nil || !strings.Contains(err.Error(), "VIDISPINE_URL") {
		t.Fatalf("relative URL: %v", err)
	}
	t.Setenv("VIDISPINE_URL", "")
	if _, err := run("vidispine", "import", vidispineID()); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("not configured: %v", err)
	}
}
