package fake

import (
	"reflect"
	"testing"

	"github.com/bcc-media/mediabrain/internal/subtitle"
)

func build(seed uint64) []any {
	x := New(seed)
	prog := x.Program()
	random := make([]byte, 16)
	_, _ = x.Read(random)
	return []any{
		x.ServiceAccount(), random, x.User(), prog, x.Project("PG-1", prog.Code), x.Location(), x.VideoFile(), x.SubtitleFile("x"),
		x.Asset(1), x.Path("mov"), x.SRT(5), x.VTT(5), x.Int(1, 9), Pick(x, "a", "b", "c"),
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
	for format, text := range map[subtitle.Format]string{subtitle.SRT: x.SRT(20), subtitle.VTT: x.VTT(20)} {
		cues, err := subtitle.Parse(format, text)
		if err != nil || len(cues) != 20 {
			t.Fatalf("%s: %d cues, %v", format, len(cues), err)
		}
	}
}
