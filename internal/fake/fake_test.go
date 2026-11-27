package fake

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bcc-media/mediabrain/internal/subtitle"
)

func build(seed uint64) []any {
	x := New(seed)
	prog := x.Program()
	return []any{
		x.ServiceAccount(), x.User(), prog, x.Project("PG-1", prog.Code), x.Location(), x.VideoFile(), x.SubtitleFile("x"),
		x.Asset(1), x.Path("mov"), x.SRT(5), x.VTT(5), x.TranscriptJSON(5), x.SubtitleFile("{}"), x.TranscriptionModel(), x.TranscriptionModels(3), x.Retranscribed(x.WordCues(5)), x.Int(1, 9), Pick(x, "a", "b", "c"),
		x.VidispineItem("VX-42"), x.VidispinePlaceholder(),
	}
}

func TestDeterministic(t *testing.T) {
	if !reflect.DeepEqual(build(42), build(42)) {
		t.Fatal("same seed must give the same data")
	}
	if reflect.DeepEqual(build(42), build(43)) {
		t.Fatal("different seeds should differ")
	}
}

func TestSubtitleTextParses(t *testing.T) {
	x := New(7)
	for format, text := range map[subtitle.Format]string{subtitle.SRT: x.SRT(20), subtitle.VTT: x.VTT(20), subtitle.JSON: x.TranscriptJSON(20)} {
		cues, err := subtitle.Parse(format, text)
		if err != nil || len(cues) != 20 {
			t.Fatalf("%s: %d cues, %v", format, len(cues), err)
		}
		if (format == subtitle.JSON) != (len(cues[0].Words) > 0) {
			t.Fatalf("%s: words = %+v", format, cues[0].Words)
		}
	}
}

func TestRetranscribed(t *testing.T) {
	x := New(11)
	heard := x.WordCues(60)
	before, _ := subtitle.Render(subtitle.JSON, heard)
	again := x.Retranscribed(heard)
	if after, _ := subtitle.Render(subtitle.JSON, heard); after != before {
		t.Fatal("the cues heard were changed")
	}
	if len(again) != len(heard) {
		t.Fatalf("%d cues, want %d", len(again), len(heard))
	}
	same := 0
	for i, c := range again {
		if c.Text == heard[i].Text {
			same++
		}
		if c.StartMs < 0 || c.EndMs <= c.StartMs || len(c.Words) != len(strings.Fields(c.Text)) {
			t.Fatalf("cue %d = %+v", i, c)
		}
	}
	// Mostly the same speech, not all of it heard alike.
	if same == 0 || same == len(heard) {
		t.Fatalf("%d of %d cues unchanged", same, len(heard))
	}

	models := x.TranscriptionModels(9)
	if len(models) != 4 || models[0] == models[1] {
		t.Fatalf("models = %v", models)
	}
	if one := x.TranscriptionModels(1); len(one) != 1 {
		t.Fatalf("one model = %v", one)
	}
}
