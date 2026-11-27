//go:build e2e

package e2e

import (
	"context"
	"errors"
	"io"
	"net/http"
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

func TestPreviewStreamsFromVidispine(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(91)
	project := newProject(t, admin, f, newProgram(t, admin, f))
	video := "a preview that is not really an mp4"

	item := f.VidispineItem(newVidispineStorage())
	item.ID = vidispineID()
	item.Files[item.Shapes[2].ContainerComponent.File[0].ID] = video
	addVidispineItem(item)
	a := importItem(t, admin, item.ID, project)
	ids := renditionIDs(t, admin, a.ID)
	preview := "/media/renditions/" + ids["ENCODE/preview"]

	// Vidispine calls it application/octet-stream; the registered file says what it is.
	got := getMedia(t, admin.token, preview, "")
	if got.status != http.StatusOK || got.body != video || got.header.Get("Content-Type") != "video/mp4" ||
		got.header.Get("Accept-Ranges") != "bytes" || got.header.Get("Cache-Control") != "private, max-age=300" {
		t.Fatalf("whole preview = %d %q %v", got.status, got.body, got.header)
	}
	got = getMedia(t, admin.token, preview, "bytes=2-8")
	if got.status != http.StatusPartialContent || got.body != video[2:9] || got.header.Get("Content-Range") != "bytes 2-8/35" {
		t.Fatalf("range = %d %q %v", got.status, got.body, got.header)
	}
	// Only a byte range is passed on.
	if got = getMedia(t, admin.token, preview, "items=1-2"); got.status != http.StatusOK || got.body != video {
		t.Fatalf("other range unit = %d %q", got.status, got.body)
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

	// Only a preview from Vidispine is served: not the master, not a preview made by hand.
	var made struct{ CreateRendition struct{ ID string } }
	admin.must(t, `mutation($v: ID!) { createRendition(versionId: $v, input: {kind: ENCODE, purpose: "preview"}) { id } }`,
		map[string]any{"v": newVersion(t, admin, a.ID)}, &made)
	for name, path := range map[string]string{
		"master":       "/media/renditions/" + ids["MASTER"],
		"hand-made":    "/media/renditions/" + made.CreateRendition.ID,
		"unknown":      "/media/renditions/RE-00000000000000000000000000",
		"not an id":    "/media/renditions/nonsense",
		"no rendition": "/media/renditions/",
	} {
		if got = getMedia(t, admin.token, path, ""); got.status != http.StatusNotFound {
			t.Errorf("%s = %d %q", name, got.status, got.body)
		}
	}

	// A preview Vidispine no longer has is missing too; a Vidispine that fails is unavailable.
	gone := vidispineItem(f, newVidispineStorage())
	b := importItem(t, admin, gone.ID, project)
	if got = getMedia(t, admin.token, "/media/renditions/"+renditionIDs(t, admin, b.ID)["ENCODE/preview"], ""); got.status != http.StatusNotFound {
		t.Fatalf("preview missing in Vidispine = %d %q", got.status, got.body)
	}
	vsServer.Fail(http.StatusBadGateway)
	got = getMedia(t, admin.token, preview, "")
	vsServer.Fail(0)
	if got.status != http.StatusServiceUnavailable || got.body != "preview unavailable\n" {
		t.Fatalf("Vidispine failing = %d %q", got.status, got.body)
	}

	// Without Vidispine there is no preview to stream, but who may see it is checked first.
	svc := media.NewService(sqlc.New(pool), db.PoolTx(pool), nil)
	system := access.With(context.Background(), access.System())
	if _, err := svc.Preview(system, ids["ENCODE/preview"], ""); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("not configured: %v", err)
	}
	if _, err := svc.Preview(context.Background(), ids["ENCODE/preview"], ""); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("nobody, not configured: %v", err)
	}
}
