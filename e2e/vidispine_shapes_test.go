//go:build e2e

package e2e

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/internal/fake"
	"github.com/bcc-media/mediabrain/internal/vidispineimport"
	"github.com/bcc-media/mediabrain/pkg/vidispine"
)

// setTranscript gives the item's transcript shape new content, as a new Vidispine file.
func setTranscript(item *fake.VidispineItem, content string) {
	f := item.Shapes[3].File()
	sum := sha1.Sum([]byte(content))
	f.ID, f.Path = vidispineID(), "transcripts/"+vidispineID()+".srt"
	f.Hash, f.Size = hex.EncodeToString(sum[:]), int64(len(content))
	item.Files = map[string]string{f.ID: content}
}

func refreshed(t *testing.T, u user, assetID string) importedAsset {
	t.Helper()
	var out struct{ RefreshFromVidispine importedAsset }
	u.must(t, refreshFromVidispine, map[string]any{"id": assetID}, &out)
	return out.RefreshFromVidispine
}

func tracksOf(a importedAsset) []string {
	var out []string
	for _, v := range a.Versions {
		for _, r := range v.Renditions {
			for _, tr := range r.SubtitleTracks {
				out = append(out, tr.SourceFileId)
			}
		}
	}
	return out
}

// A refresh is a re-import: it adds what Vidispine has and the asset lacks, and nothing twice.
func TestVidispineReimport(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(74)
	storage := newVidispineStorage()

	// Imported as a placeholder, the item gets its files in Vidispine later.
	item := f.VidispineItem(storage)
	item.ID = vidispineID()
	vsServer.AddItem(item.ID, item.Fields)
	a := importItem(t, admin, item.ID, "")
	if len(a.Versions) != 0 {
		t.Fatalf("placeholder = %+v", a.Versions)
	}
	addVidispineItem(item)
	got := refreshed(t, admin, a.ID)
	if kinds := renditionsByKind(got); len(got.Versions) != 1 || len(kinds) != 3 || len(tracksOf(got)) != 1 {
		t.Fatalf("after the files came: %v %+v", kinds, got.Versions)
	}

	again := refreshed(t, admin, a.ID)
	if len(again.Versions) != 1 || len(again.Versions[0].Renditions) != 3 || len(tracksOf(again)) != 1 {
		t.Fatalf("a second refresh added something: %+v", again.Versions)
	}

	// A new transcript becomes the next revision; a new preview does not replace the old one.
	setTranscript(&item, f.SRT(4))
	item.Shapes[2].ContainerComponent.File[0].Hash = hex.EncodeToString(make([]byte, 20))
	addVidispineItem(item)
	got = refreshed(t, admin, a.ID)
	subs := got.Versions[0].Renditions[renditionsByKind(got)["SUBTITLE"]]
	if len(got.Versions[0].Renditions) != 3 || len(subs.SubtitleTracks) != 2 || subs.SubtitleTracks[0].Revision != 2 ||
		subs.SubtitleTracks[0].Source != "Mediabanken" || len(subs.Files) != 2 {
		t.Fatalf("after a new transcript: %+v", got.Versions[0].Renditions)
	}
	preview := got.Versions[0].Renditions[renditionsByKind(got)["ENCODE/preview"]]
	if preview.Files[0].File.Hashes[0].Value == item.Shapes[2].ContainerComponent.File[0].Hash {
		t.Fatal("the preview was replaced")
	}
}

// The preview and the transcript are extras: one that cannot be used is left out and the rest
// is imported.
func TestVidispineExtrasLeftOut(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(75)
	storage := newVidispineStorage()
	noAddress := vidispineID()
	vsServer.AddStorage(noAddress)

	cases := map[string]struct {
		change      func(*fake.VidispineItem)
		wantPreview bool
	}{
		"preview still open":          {func(it *fake.VidispineItem) { it.Shapes[2].ContainerComponent.File[0].State = "OPEN" }, false},
		"preview on unusable storage": {func(it *fake.VidispineItem) { it.Shapes[2].ContainerComponent.File[0].Storage = noAddress }, false},
		"transcript not readable":     {func(it *fake.VidispineItem) { it.Files = nil }, true},
		"transcript not SRT":          {func(it *fake.VidispineItem) { setTranscript(it, "just words, no timing") }, true},
		"transcript hash differs": {func(it *fake.VidispineItem) {
			for id := range it.Files {
				it.Files[id] += "\n\n99\n00:00:01,000 --> 00:00:02,000\nadded\n"
			}
		}, true},
		"transcript without hash": {func(it *fake.VidispineItem) { it.Shapes[3].File().Hash = "" }, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			item := f.VidispineItem(storage)
			item.ID = vidispineID()
			c.change(&item)
			addVidispineItem(item)
			a := importItem(t, admin, item.ID, "")
			kinds := renditionsByKind(a)
			_, hasPreview := kinds["ENCODE/preview"]
			_, hasSubs := kinds["SUBTITLE"]
			if _, ok := kinds["MASTER"]; !ok || hasPreview != c.wantPreview || hasSubs == c.wantPreview {
				t.Fatalf("renditions = %v", kinds)
			}
		})
	}
}

// filesDown is a Vidispine whose files cannot be read, while everything else answers.
type filesDown struct{ *vidispine.Client }

func (filesDown) ReadFile(context.Context, string, int64) ([]byte, error) {
	return nil, errors.New("connection reset")
}

func TestVidispineTranscriptUnreachable(t *testing.T) {
	f := fake.New(76)
	item := vidispineItem(f, newVidispineStorage())
	client, err := vidispine.New(vsServer.Config())
	if err != nil {
		t.Fatal(err)
	}
	svc := vidispineimport.NewService(sqlc.New(pool), db.PoolTx(pool), filesDown{client})
	ctx := access.With(context.Background(), access.System())
	if _, err := svc.Import(ctx, vidispineimport.ImportInput{ItemID: item.ID, TypeID: 1}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("import: %v", err)
	}
	if n := count(t, `SELECT count(*) FROM asset WHERE legacy_ids @> ARRAY[$1]`, item.ID); n != 0 {
		t.Fatalf("%d assets left behind", n)
	}
}
