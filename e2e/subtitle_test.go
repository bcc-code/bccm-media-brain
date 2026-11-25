//go:build e2e

package e2e

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bcc-media/mediabrain/internal/fake"
	"github.com/bcc-media/mediabrain/internal/subtitle"
)

const (
	importSubtitles = `mutation($in: ImportSubtitlesInput!) { importSubtitles(input: $in) {
		id renditionId assetId sourceFileId language kind origin model revision parentId createdBy
	} }`
	editSubtitles = `mutation($id: ID!, $cues: [SubtitleCueInput!]!) { editSubtitles(trackId: $id, cues: $cues) { id revision parentId origin model language kind } }`
	trackCues     = `query($id: ID!, $n: Int, $c: String) { subtitleTrack(id: $id) { id revision cues(first: $n, after: $c) { nodes { seq startMs endMs text speaker } pageInfo { hasNextPage endCursor } } } }`
)

type trackJSON struct {
	ID, RenditionID, AssetID, Kind, Origin  string
	Language, SourceFileID, ParentID, Model *string
	CreatedBy                               *string
	Revision                                int
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
		first.Origin != "IMPORT" || first.Model != nil || first.Kind != "SUBTITLE" || *first.Language != "en" || *first.SourceFileID != source || *first.CreatedBy != admin.id {
		t.Fatalf("first import = %+v", first)
	}
	if first.RenditionID == c.rendition {
		t.Fatal("subtitles must get their own rendition, not the master")
	}

	var got struct {
		SubtitleTrack struct {
			Cues nodes[cueJSON]
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
	// Ten cues, then what follows them: a page that is cut says so, which is what keeps an
	// editor from saving part of a track as the whole.
	var paged struct {
		SubtitleTrack struct {
			Cues struct {
				Nodes    []cueJSON
				PageInfo pageInfo
			}
		}
	}
	admin.must(t, trackCues, map[string]any{"id": first.ID, "n": 10}, &paged)
	if c := paged.SubtitleTrack.Cues; len(c.Nodes) != 10 || !c.PageInfo.HasNextPage {
		t.Fatalf("the first ten cues = %+v", c)
	}
	admin.must(t, trackCues, map[string]any{"id": first.ID, "n": 5, "c": paged.SubtitleTrack.Cues.PageInfo.EndCursor}, &paged)
	if c := paged.SubtitleTrack.Cues; len(c.Nodes) != 2 || c.Nodes[0].Seq != 11 || c.PageInfo.HasNextPage {
		t.Fatalf("the cues after the tenth = %+v", c)
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
			Versions nodes[struct {
				Renditions []struct {
					ID, Kind       string
					Files          []struct{ Role string }
					SubtitleTracks []trackJSON
					All            []trackJSON
				}
			}]
		}
	}
	admin.must(t, `query($a: ID!) {
		latest: subtitleTracks(assetId: $a) { id revision }
		history: subtitleTracks(assetId: $a, history: true) { id revision }
		asset(id: $a) { versions { nodes { renditions { id kind files { role } subtitleTracks { id revision } all: subtitleTracks(history: true) { id } } } } }
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
		"manual origin":    imp(map[string]any{"origin": "MANUAL"}),
		"long model":       imp(map[string]any{"model": strings.Repeat("x", subtitle.MaxModelLength+1)}),
		"broken json":      imp(map[string]any{"format": "JSON", "content": `{"segments": [`}),
		"srt as json":      imp(map[string]any{"format": "JSON"}),
		"bad word":         imp(map[string]any{"format": "JSON", "content": `{"segments": [{"start": 0, "end": 1, "text": "x", "words": [{"text": "x", "start": 0.5, "end": 0.1}]}]}`}),
	} {
		t.Run(name, func(t *testing.T) { admin.fails(t, "INVALID_INPUT", importSubtitles, vars) })
	}
	admin.fails(t, "NOT_FOUND", importSubtitles, imp(map[string]any{"versionId": "AV-00000000000000000000000000"}))

	var out struct{ ImportSubtitles trackJSON }
	admin.must(t, importSubtitles, imp(nil), &out)
	track := out.ImportSubtitles.ID
	for name, cues := range map[string][]map[string]any{
		"none":              {},
		"no text":           {{"startMs": 0, "endMs": 10, "text": " "}},
		"negative":          {{"startMs": -5, "endMs": 10, "text": "x"}},
		"reversed":          {{"startMs": 10, "endMs": 10, "text": "x"}},
		"word without text": {{"startMs": 0, "endMs": 10, "text": "x", "words": []map[string]any{{"startMs": 0, "endMs": 5, "text": " "}}}},
		"word confidence":   {{"startMs": 0, "endMs": 10, "text": "x", "words": []map[string]any{{"startMs": 0, "endMs": 5, "text": "x", "confidence": 2}}}},
	} {
		t.Run("edit/"+name, func(t *testing.T) {
			admin.fails(t, "INVALID_INPUT", editSubtitles, map[string]any{"id": track, "cues": cues})
		})
	}
	admin.fails(t, "NOT_FOUND", editSubtitles, map[string]any{"id": "ST-00000000000000000000000000", "cues": []map[string]any{{"startMs": 0, "endMs": 1, "text": "x"}}})
	admin.fails(t, "INVALID_INPUT", trackCues, map[string]any{"id": track, "n": 5001})
	admin.fails(t, "NOT_FOUND", `{ subtitleTracks(assetId: "AS-00000000000000000000000000") { id } }`, nil)
	var rev struct{ SubtitleTrack struct{ Revision int } }
	admin.must(t, trackCues, map[string]any{"id": track, "n": 5000}, &rev)
	if rev.SubtitleTrack.Revision != 1 {
		t.Fatal("failed edits must not add revisions")
	}
}

// An ASR import records its model; an edit of it is MANUAL and has none, while the ASR
// revision keeps its own.
func TestSubtitleOriginAndModel(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(1311)
	c := newClip(t, admin, f, newProject(t, admin, f, newProgram(t, admin, f)), "")
	var imp struct{ ImportSubtitles trackJSON }
	admin.must(t, importSubtitles, map[string]any{"in": map[string]any{
		"versionId": c.version, "language": "nb", "kind": "TRANSCRIPT", "format": "VTT", "content": f.VTT(3),
		"origin": "ASR", "model": "  nb-whisper-large ",
	}}, &imp)
	asr := imp.ImportSubtitles
	if asr.Origin != "ASR" || asr.Model == nil || *asr.Model != "nb-whisper-large" || asr.Kind != "TRANSCRIPT" {
		t.Fatalf("asr import = %+v", asr)
	}

	var ed struct{ EditSubtitles trackJSON }
	admin.must(t, editSubtitles, map[string]any{"id": asr.ID, "cues": []map[string]any{{"startMs": 0, "endMs": 1000, "text": "corrected"}}}, &ed)
	if ed.EditSubtitles.Origin != "MANUAL" || ed.EditSubtitles.Model != nil {
		t.Fatalf("edit of an asr track = %+v", ed.EditSubtitles)
	}
	var got struct{ History []trackJSON }
	admin.must(t, `query($a: ID!) { history: subtitleTracks(assetId: $a, history: true) { id origin model revision } }`,
		map[string]any{"a": c.asset}, &got)
	if len(got.History) != 2 || got.History[1].ID != asr.ID || got.History[1].Model == nil || *got.History[1].Model != "nb-whisper-large" {
		t.Fatalf("history = %+v", got.History)
	}

	// A blank model is no model, and an import without an origin is IMPORT.
	admin.must(t, importSubtitles, map[string]any{"in": map[string]any{
		"versionId": c.version, "language": "nb", "format": "SRT", "content": f.SRT(2), "model": " ",
	}}, &imp)
	if imp.ImportSubtitles.Origin != "IMPORT" || imp.ImportSubtitles.Model != nil || imp.ImportSubtitles.Revision != 3 {
		t.Fatalf("plain import = %+v", imp.ImportSubtitles)
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

// Word timing from a Whisper JSON transcript is stored with each cue, read back through
// words, kept by a JSON export and by an edit that sends it, and dropped by one that does not.
func TestSubtitleWordTiming(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(1125)
	c := newClip(t, admin, f, newProject(t, admin, f, newProgram(t, admin, f)), "")
	content := f.TranscriptJSON(6)
	want, err := subtitle.Parse(subtitle.JSON, content)
	if err != nil {
		t.Fatal(err)
	}
	var imp struct{ ImportSubtitles trackJSON }
	admin.must(t, importSubtitles, map[string]any{"in": map[string]any{
		"versionId": c.version, "language": "nb", "kind": "TRANSCRIPT", "format": "JSON", "content": content,
		"origin": "ASR", "model": "nb-whisper-large",
	}}, &imp)
	track := imp.ImportSubtitles.ID

	const withWords = `query($id: ID!) { subtitleTrack(id: $id) {
		cues { nodes { startMs endMs text words { startMs endMs text confidence } } }
		export(format: JSON)
	} }`
	type wordsJSON struct {
		SubtitleTrack struct {
			Cues struct {
				Nodes []struct {
					StartMs, EndMs int32
					Text           string
					Words          []subtitle.Word
				}
			}
			Export string
		}
	}
	var got wordsJSON
	admin.must(t, withWords, map[string]any{"id": track}, &got)
	nodes := got.SubtitleTrack.Cues.Nodes
	if len(nodes) != len(want) {
		t.Fatalf("%d cues, want %d", len(nodes), len(want))
	}
	for i, n := range nodes {
		if n.Text != want[i].Text || len(n.Words) == 0 || !reflect.DeepEqual(n.Words, want[i].Words) {
			t.Fatalf("cue %d = %+v, want %+v", i, n, want[i])
		}
	}
	if back, err := subtitle.Parse(subtitle.JSON, got.SubtitleTrack.Export); err != nil || !reflect.DeepEqual(back, want) {
		t.Fatalf("export = %v, %v", got.SubtitleTrack.Export, err)
	}

	// An edit keeps the words it is sent and drops those it is not.
	first := nodes[0]
	words := make([]map[string]any, len(first.Words))
	for i, w := range first.Words {
		words[i] = map[string]any{"startMs": w.StartMs, "endMs": w.EndMs, "text": w.Text, "confidence": w.Confidence}
	}
	var ed struct{ EditSubtitles trackJSON }
	admin.must(t, editSubtitles, map[string]any{"id": track, "cues": []map[string]any{
		{"startMs": first.StartMs, "endMs": first.EndMs, "text": first.Text, "words": words},
		{"startMs": first.EndMs + 1000, "endMs": first.EndMs + 2000, "text": "corrected by hand"},
	}}, &ed)
	admin.must(t, withWords, map[string]any{"id": ed.EditSubtitles.ID}, &got)
	nodes = got.SubtitleTrack.Cues.Nodes
	if len(nodes) != 2 || !reflect.DeepEqual(nodes[0].Words, first.Words) || nodes[1].Words != nil {
		t.Fatalf("edited = %+v", nodes)
	}

	// Subtitles without word timing have none.
	admin.must(t, importSubtitles, map[string]any{"in": map[string]any{
		"versionId": c.version, "language": "en", "format": "SRT", "content": f.SRT(2),
	}}, &imp)
	admin.must(t, withWords, map[string]any{"id": imp.ImportSubtitles.ID}, &got)
	for _, n := range got.SubtitleTrack.Cues.Nodes {
		if n.Words != nil {
			t.Fatalf("srt cue has words: %+v", n)
		}
	}
	if !strings.Contains(got.SubtitleTrack.Export, `"segments"`) || strings.Contains(got.SubtitleTrack.Export, `"words"`) {
		t.Fatalf("srt export as json = %s", got.SubtitleTrack.Export)
	}
}
