//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/bcc-media/mediabrain/internal/fake"
	"github.com/bcc-media/mediabrain/internal/subtitle"
)

const (
	importSubtitles = `mutation($in: ImportSubtitlesInput!) { importSubtitles(input: $in) {
		id renditionId assetId sourceFileId language kind origin revision parentId createdBy
	} }`
	editSubtitles = `mutation($id: ID!, $cues: [SubtitleCueInput!]!) { editSubtitles(trackId: $id, cues: $cues) { id revision parentId origin language kind } }`
	trackCues     = `query($id: ID!, $l: Int, $o: Int) { subtitleTrack(id: $id) { id revision cues(limit: $l, offset: $o) { seq startMs endMs text speaker } } }`
)

type trackJSON struct {
	ID, RenditionID, AssetID, Kind, Origin string
	Language, SourceFileID, ParentID       *string
	CreatedBy                              *string
	Revision                               int
}

type cueJSON struct {
	Seq, StartMs, EndMs int
	Text                string
	Speaker             *string
}

func TestSubtitleImportEditExport(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(9)
	prog := newProgram(t, admin, f)
	c := newClip(t, admin, f, newProject(t, admin, f, prog), "")

	vtt := f.VTT(12)
	source := newFile(t, admin, f, "")
	var imp struct{ ImportSubtitles trackJSON }
	importVars := map[string]any{"in": map[string]any{
		"versionId": c.version, "language": "en", "format": "VTT", "content": vtt, "sourceFileId": source,
	}}
	admin.must(t, importSubtitles, importVars, &imp)
	first := imp.ImportSubtitles
	if !strings.HasPrefix(first.ID, "ST-") || first.AssetID != c.asset || first.Revision != 1 || first.ParentID != nil ||
		first.Origin != "IMPORT" || first.Kind != "SUBTITLE" || *first.Language != "en" || *first.SourceFileID != source || *first.CreatedBy != admin.id {
		t.Fatalf("first import = %+v", first)
	}
	if first.RenditionID == c.rendition {
		t.Fatal("subtitles must get their own rendition, not the master")
	}

	var got struct {
		SubtitleTrack struct {
			Cues []cueJSON
		}
	}
	admin.must(t, trackCues, map[string]any{"id": first.ID}, &got)
	want, _ := subtitle.Parse(subtitle.VTT, vtt)
	if len(got.SubtitleTrack.Cues) != 12 {
		t.Fatalf("%d cues", len(got.SubtitleTrack.Cues))
	}
	for i, cue := range got.SubtitleTrack.Cues {
		w := want[i]
		if cue.Seq != i+1 || cue.StartMs != int(w.StartMs) || cue.EndMs != int(w.EndMs) || cue.Text != w.Text || (cue.Speaker == nil) != (w.Speaker == nil) {
			t.Fatalf("cue %d = %+v, want %+v", i, cue, w)
		}
	}
	admin.must(t, trackCues, map[string]any{"id": first.ID, "l": 5, "o": 10}, &got)
	if len(got.SubtitleTrack.Cues) != 2 || got.SubtitleTrack.Cues[0].Seq != 11 {
		t.Fatalf("paged cues = %+v", got.SubtitleTrack.Cues)
	}

	// Importing the same language again adds revision 2 on the same rendition; the source
	// file is attached to the rendition once.
	admin.must(t, importSubtitles, importVars, &imp)
	second := imp.ImportSubtitles
	if second.Revision != 2 || *second.ParentID != first.ID || second.RenditionID != first.RenditionID {
		t.Fatalf("second import = %+v", second)
	}
	// Another language gets its own rendition and starts at revision 1.
	admin.must(t, importSubtitles, map[string]any{"in": map[string]any{
		"versionId": c.version, "language": "nb", "kind": "CC", "format": "SRT", "content": f.SRT(3),
	}}, &imp)
	norwegian := imp.ImportSubtitles
	if norwegian.Revision != 1 || norwegian.RenditionID == first.RenditionID || norwegian.Kind != "CC" || norwegian.SourceFileID != nil {
		t.Fatalf("norwegian = %+v", norwegian)
	}

	// Editing writes revision 3 with exactly the given cues, reordered by start time.
	var ed struct{ EditSubtitles trackJSON }
	cues := []map[string]any{
		{"startMs": 5000, "endMs": 6000, "text": "second line", "speaker": "Bo"},
		{"startMs": 1000, "endMs": 2000, "text": "  first line  "},
	}
	admin.must(t, editSubtitles, map[string]any{"id": second.ID, "cues": cues}, &ed)
	third := ed.EditSubtitles
	if third.Revision != 3 || *third.ParentID != second.ID || third.Origin != "MANUAL" || *third.Language != "en" {
		t.Fatalf("edit = %+v", third)
	}
	admin.must(t, trackCues, map[string]any{"id": third.ID}, &got)
	if len(got.SubtitleTrack.Cues) != 2 || got.SubtitleTrack.Cues[0].Text != "first line" || *got.SubtitleTrack.Cues[1].Speaker != "Bo" {
		t.Fatalf("edited cues = %+v", got.SubtitleTrack.Cues)
	}
	// The earlier revisions are untouched.
	admin.must(t, trackCues, map[string]any{"id": first.ID}, &got)
	if len(got.SubtitleTrack.Cues) != 12 {
		t.Fatalf("revision 1 changed: %d cues", len(got.SubtitleTrack.Cues))
	}
	// Only the latest revision can be edited.
	admin.fails(t, "CONFLICT", editSubtitles, map[string]any{"id": second.ID, "cues": cues})

	// Export in both formats.
	var exp struct {
		SubtitleTrack struct{ Srt, Vtt string }
	}
	admin.must(t, `query($id: ID!) { subtitleTrack(id: $id) { srt: export(format: SRT) vtt: export(format: VTT) } }`, map[string]any{"id": third.ID}, &exp)
	if exp.SubtitleTrack.Srt != "1\n00:00:01,000 --> 00:00:02,000\nfirst line\n\n2\n00:00:05,000 --> 00:00:06,000\nsecond line\n\n" {
		t.Fatalf("srt = %q", exp.SubtitleTrack.Srt)
	}
	if exp.SubtitleTrack.Vtt != "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nfirst line\n\n00:00:05.000 --> 00:00:06.000\n<v Bo>second line\n\n" {
		t.Fatalf("vtt = %q", exp.SubtitleTrack.Vtt)
	}

	// Listing: the latest revision per rendition, or everything with history.
	var list struct {
		Latest, History []trackJSON
		Asset           struct {
			Versions []struct {
				Renditions []struct {
					ID, Kind       string
					Files          []struct{ Role string }
					SubtitleTracks []trackJSON
					All            []trackJSON
				}
			}
		}
	}
	admin.must(t, `query($a: ID!) {
		latest: subtitleTracks(assetId: $a) { id revision }
		history: subtitleTracks(assetId: $a, history: true) { id revision }
		asset(id: $a) { versions { renditions { id kind files { role } subtitleTracks { id revision } all: subtitleTracks(history: true) { id } } } }
	}`, map[string]any{"a": c.asset}, &list)
	if len(list.Latest) != 2 || len(list.History) != 4 {
		t.Fatalf("latest %d, history %d", len(list.Latest), len(list.History))
	}
	for _, r := range list.Asset.Versions[0].Renditions {
		switch r.ID {
		case first.RenditionID:
			if len(r.SubtitleTracks) != 1 || r.SubtitleTracks[0].ID != third.ID || len(r.All) != 3 || len(r.Files) != 1 || r.Files[0].Role != "SUBTITLE" {
				t.Fatalf("english rendition = %+v", r)
			}
		case c.rendition:
			if len(r.SubtitleTracks) != 0 {
				t.Fatalf("master rendition has tracks: %+v", r)
			}
		}
	}

	// Deleting the latest revision makes the one before it the latest again.
	const del = `mutation($id: ID!) { deleteSubtitleTrack(id: $id) }`
	admin.must(t, del, map[string]any{"id": third.ID}, nil)
	admin.isNull(t, "subtitleTrack", third.ID)
	admin.fails(t, "NOT_FOUND", del, map[string]any{"id": third.ID})
	admin.must(t, editSubtitles, map[string]any{"id": second.ID, "cues": cues}, &ed)
	if ed.EditSubtitles.Revision != 3 {
		t.Fatalf("revision after delete and edit = %d", ed.EditSubtitles.Revision)
	}
}

func TestSubtitleValidation(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(10)
	prog := newProgram(t, admin, f)
	c := newClip(t, admin, f, newProject(t, admin, f, prog), "")
	imp := func(patch map[string]any) map[string]any {
		in := map[string]any{"versionId": c.version, "language": "en", "format": "SRT", "content": f.SRT(2)}
		for k, v := range patch {
			in[k] = v
		}
		return map[string]any{"in": in}
	}
	for name, vars := range map[string]map[string]any{
		"empty":            imp(map[string]any{"content": ""}),
		"not subtitles":    imp(map[string]any{"content": "just some text\nwithout timing"}),
		"reversed times":   imp(map[string]any{"content": "1\n00:00:02,000 --> 00:00:01,000\nx\n"}),
		"unknown language": imp(map[string]any{"language": "klingon"}),
		"unknown source":   imp(map[string]any{"sourceFileId": "FI-00000000000000000000000000"}),
	} {
		t.Run(name, func(t *testing.T) { admin.fails(t, "INVALID_INPUT", importSubtitles, vars) })
	}
	admin.fails(t, "NOT_FOUND", importSubtitles, imp(map[string]any{"versionId": "AV-00000000000000000000000000"}))

	var out struct{ ImportSubtitles trackJSON }
	admin.must(t, importSubtitles, imp(nil), &out)
	track := out.ImportSubtitles.ID
	for name, cues := range map[string][]map[string]any{
		"none":     {},
		"no text":  {{"startMs": 0, "endMs": 10, "text": " "}},
		"negative": {{"startMs": -5, "endMs": 10, "text": "x"}},
		"reversed": {{"startMs": 10, "endMs": 10, "text": "x"}},
	} {
		t.Run("edit/"+name, func(t *testing.T) {
			admin.fails(t, "INVALID_INPUT", editSubtitles, map[string]any{"id": track, "cues": cues})
		})
	}
	admin.fails(t, "NOT_FOUND", editSubtitles, map[string]any{"id": "ST-00000000000000000000000000", "cues": []map[string]any{{"startMs": 0, "endMs": 1, "text": "x"}}})
	admin.fails(t, "INVALID_INPUT", trackCues, map[string]any{"id": track, "l": 5001})
	admin.fails(t, "NOT_FOUND", `{ subtitleTracks(assetId: "AS-00000000000000000000000000") { id } }`, nil)
	var rev struct{ SubtitleTrack struct{ Revision int } }
	admin.must(t, trackCues, map[string]any{"id": track, "l": 5000}, &rev)
	if rev.SubtitleTrack.Revision != 1 {
		t.Fatal("failed edits must not add revisions")
	}
}

// A track's language is a copy of its rendition's; changing the rendition changes the copy.
func TestSubtitleLanguageFollowsItsRendition(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(1210)
	c := newClip(t, admin, f, newProject(t, admin, f, newProgram(t, admin, f)), "")
	var imp struct{ ImportSubtitles trackJSON }
	admin.must(t, importSubtitles, map[string]any{"in": map[string]any{
		"versionId": c.version, "language": "en", "format": "VTT", "content": f.VTT(2),
	}}, &imp)
	admin.must(t, `mutation($id: ID!) { updateRendition(id: $id, input: {language: "nb"}) { id } }`,
		map[string]any{"id": imp.ImportSubtitles.RenditionID}, nil)
	var got struct{ SubtitleTrack struct{ Language *string } }
	admin.must(t, `query($id: ID!) { subtitleTrack(id: $id) { language } }`, map[string]any{"id": imp.ImportSubtitles.ID}, &got)
	if got.SubtitleTrack.Language == nil || *got.SubtitleTrack.Language != "nb" {
		t.Fatalf("the track's language is %v, its rendition's is nb", got.SubtitleTrack.Language)
	}
}
