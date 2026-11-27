//go:build e2e

package e2e

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/internal/fake"
	"github.com/bcc-media/mediabrain/internal/media"
)

type mediaAnswer struct {
	status int
	header http.Header
	body   string
}

// getMedia fetches a path as the user (token "" for nobody), with a Range header if given.
func getMedia(t *testing.T, token, path, rangeHeader string) mediaAnswer {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, baseURL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return mediaAnswer{res.StatusCode, res.Header, string(body)}
}

// renditionIDs keys the renditions of an asset by kind and purpose ("ENCODE/preview").
func renditionIDs(t *testing.T, u user, assetID string) map[string]string {
	t.Helper()
	var out struct {
		Asset struct {
			Versions nodes[struct {
				Renditions []struct{ ID, Kind, Purpose string }
			}]
		}
	}
	u.must(t, `query($id: ID!) { asset(id: $id) { versions { nodes { renditions { id kind purpose } } } } }`, map[string]any{"id": assetID}, &out)
	ids := map[string]string{}
	for _, v := range out.Asset.Versions {
		for _, r := range v.Renditions {
			key := r.Kind
			if r.Purpose != "" {
				key += "/" + r.Purpose
			}
			ids[key] = r.ID
		}
	}
	return ids
}

// previewItem adds an item to the fake Vidispine whose storage is a directory of this test, and
// writes its preview there unless content is empty.
func previewItem(t *testing.T, f *fake.Faker, content string, uris ...string) fake.VidispineItem {
	t.Helper()
	dir := t.TempDir()
	storage := vidispineID()
	vsServer.AddStorage(storage, append(uris, "file://"+dir+"/")...)
	item := f.VidispineItem(storage)
	item.ID = vidispineID()
	addVidispineItem(item)
	if content != "" {
		path := filepath.Join(dir, filepath.FromSlash(item.Shapes[2].ContainerComponent.File[0].Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return item
}

func TestPreviewIsReadFromStorage(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(91)
	project := newProject(t, admin, f, newProgram(t, admin, f))
	video := "a preview that is not really an mp4"

	a := importItem(t, admin, previewItem(t, f, video).ID, project)
	ids := renditionIDs(t, admin, a.ID)
	preview := "/media/renditions/" + ids["ENCODE/preview"]

	// Vidispine is not asked: it may be down while the preview plays.
	vsServer.Fail(http.StatusBadGateway)
	got := getMedia(t, admin.token, preview, "")
	if got.status != http.StatusOK || got.body != video || got.header.Get("Content-Type") != "video/mp4" ||
		got.header.Get("Accept-Ranges") != "bytes" || got.header.Get("Cache-Control") != "private, max-age=300" {
		vsServer.Fail(0)
		t.Fatalf("whole preview = %d %q %v", got.status, got.body, got.header)
	}
	vsServer.Fail(0)
	got = getMedia(t, admin.token, preview, "bytes=2-8")
	if got.status != http.StatusPartialContent || got.body != video[2:9] || got.header.Get("Content-Range") != "bytes 2-8/35" {
		t.Fatalf("range = %d %q %v", got.status, got.body, got.header)
	}

	// Anyone who may view the asset may watch; for anyone else it does not exist.
	viewer, stranger := signIn(t), signIn(t)
	grant(t, admin, "PROJECT", project, "VIEW", to(viewer))
	if got = getMedia(t, viewer.token, preview, ""); got.status != http.StatusOK || got.body != video {
		t.Fatalf("viewer = %d %q", got.status, got.body)
	}
	if got = getMedia(t, stranger.token, preview, ""); got.status != http.StatusNotFound || got.body == video {
		t.Fatalf("stranger = %d %q", got.status, got.body)
	}
	if got = getMedia(t, "", preview, ""); got.status != http.StatusUnauthorized {
		t.Fatalf("nobody = %d", got.status)
	}

	// Only a preview with a video file is served: not the master, not a preview without a file.
	var made struct{ CreateRendition struct{ ID string } }
	admin.must(t, `mutation($v: ID!) { createRendition(versionId: $v, input: {kind: ENCODE, purpose: "preview"}) { id } }`,
		map[string]any{"v": newVersion(t, admin, a.ID)}, &made)
	for name, path := range map[string]string{
		"master":       "/media/renditions/" + ids["MASTER"],
		"no file":      "/media/renditions/" + made.CreateRendition.ID,
		"unknown":      "/media/renditions/RE-00000000000000000000000000",
		"not an id":    "/media/renditions/nonsense",
		"no rendition": "/media/renditions/",
	} {
		if got = getMedia(t, admin.token, path, ""); got.status != http.StatusNotFound {
			t.Errorf("%s = %d %q", name, got.status, got.body)
		}
	}

	// A copy that cannot be read here makes the preview unavailable: the file is not there, the
	// location is offline, the copy is known to be missing, or the storage has no file:// address.
	unavailable := func(name, path string) {
		t.Helper()
		if got := getMedia(t, admin.token, path, ""); got.status != http.StatusServiceUnavailable || got.body != "preview unavailable\n" {
			t.Errorf("%s = %d %q", name, got.status, got.body)
		}
	}
	gone := importItem(t, admin, previewItem(t, f, "").ID, project)
	unavailable("file not there", "/media/renditions/"+renditionIDs(t, admin, gone.ID)["ENCODE/preview"])

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	const copyOf = `SELECT c.id FROM file_copy c JOIN rendition_file rf ON rf.file_id = c.file_id WHERE rf.rendition_id = $1`
	exec(`UPDATE storage_location SET online = false WHERE id IN (SELECT location_id FROM file_copy WHERE id IN (`+copyOf+`))`, ids["ENCODE/preview"])
	unavailable("offline", preview)
	exec(`UPDATE storage_location SET online = true WHERE id IN (SELECT location_id FROM file_copy WHERE id IN (`+copyOf+`))`, ids["ENCODE/preview"])
	exec(`UPDATE file_copy SET status = 'missing' WHERE id IN (`+copyOf+`)`, ids["ENCODE/preview"])
	unavailable("copy missing", preview)
	exec(`UPDATE file_copy SET status = 'present' WHERE id IN (`+copyOf+`)`, ids["ENCODE/preview"])

	s3 := f.VidispineItem(vidispineID())
	s3.ID = vidispineID()
	vsServer.AddStorage(s3.Shapes[0].ContainerComponent.File[0].Storage, "s3://bucket-"+s3.ID+"/")
	addVidispineItem(s3)
	onS3 := importItem(t, admin, s3.ID, project)
	unavailable("s3 only", "/media/renditions/"+renditionIDs(t, admin, onS3.ID)["ENCODE/preview"])

	if got = getMedia(t, admin.token, preview, ""); got.status != http.StatusOK {
		t.Fatalf("restored = %d", got.status)
	}
	svc := media.NewService(sqlc.New(pool), db.PoolTx(pool))
	if _, err := svc.Preview(context.Background(), ids["ENCODE/preview"]); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("nobody: %v", err)
	}
	f2, err := svc.Preview(access.With(context.Background(), access.System()), ids["ENCODE/preview"])
	if err != nil || f2.MimeType != "video/mp4" {
		t.Fatalf("system: %+v %v", f2, err)
	}
	_ = f2.Close()
}
