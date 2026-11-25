package subtitle

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bcc-media/mediabrain/internal/domain"
)

// As the transcriber writes it, with openai-whisper's spelling on the second segment.
const whisper = "\ufeff" + `{"text": " Hei og velkommen. Takk", "language": "nb", "segments": [
 {"id": 0, "start": 0.0004, "end": 2.5, "text": " Hei og velkommen.", "avg_logprob": -0.2,
  "words": [{"text": " og", "start": 0.6, "end": 0.8}, {"text": " Hei", "start": 0.12, "end": 0.5, "confidence": 0.93},
            {"text": " ", "start": 1, "end": 1}, {"text": " velkommen.", "start": 0.9, "end": 2.6}]},
 {"id": 1, "start": 3.25, "end": 3.0, "text": "   "},
 {"id": 2, "start": 3.0, "end": 4.0, "text": "Takk", "speaker": "Anna",
  "words": [{"word": "Takk", "start": 3.1, "end": 3.1, "probability": 0.5}]},
 {"id": 3, "start": 5, "end": 6, "text": "Ingen ord"}]}`

func TestParseJSON(t *testing.T) {
	cues, err := Parse(JSON, whisper)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 3 {
		t.Fatalf("cues = %+v", cues)
	}
	first := cues[0]
	if first.StartMs != 0 || first.EndMs != 2500 || first.Text != "Hei og velkommen." || first.Speaker != nil {
		t.Fatalf("first = %+v", first)
	}
	want := []Word{
		{StartMs: 120, EndMs: 500, Text: "Hei", Confidence: ptr(0.93)},
		{StartMs: 600, EndMs: 800, Text: "og"},
		{StartMs: 900, EndMs: 2600, Text: "velkommen."},
	}
	if !reflect.DeepEqual(first.Words, want) {
		t.Fatalf("words = %+v", first.Words)
	}
	takk := cues[1]
	if *takk.Speaker != "Anna" || !reflect.DeepEqual(takk.Words, []Word{{StartMs: 3100, EndMs: 3100, Text: "Takk", Confidence: ptr(0.5)}}) {
		t.Fatalf("takk = %+v", takk)
	}
	if cues[2].Words != nil {
		t.Fatalf("no words = %+v", cues[2].Words)
	}
}

func TestParseJSONRejects(t *testing.T) {
	for name, content := range map[string]string{
		"not json":           `WEBVTT`,
		"no segments":        `{"text": ""}`,
		"negative start":     `{"segments": [{"start": -1, "end": 1, "text": "a"}]}`,
		"huge end":           `{"segments": [{"start": 0, "end": 1e10, "text": "a"}]}`,
		"end before start":   `{"segments": [{"start": 2, "end": 1, "text": "a"}]}`,
		"negative word":      `{"segments": [{"start": 0, "end": 1, "text": "a", "words": [{"text": "a", "start": -0.5, "end": 0.2}]}]}`,
		"word ends early":    `{"segments": [{"start": 0, "end": 1, "text": "a", "words": [{"text": "a", "start": 0.5, "end": 0.2}]}]}`,
		"bad confidence":     `{"segments": [{"start": 0, "end": 1, "text": "a", "words": [{"text": "a", "start": 0, "end": 0.2, "confidence": 1.5}]}]}`,
		"negative certainty": `{"segments": [{"start": 0, "end": 1, "text": "a", "words": [{"text": "a", "start": 0, "end": 0.2, "probability": -0.1}]}]}`,
	} {
		if _, err := Parse(JSON, content); !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestNormalizeWords(t *testing.T) {
	many := make([]Word, MaxWordsPerCue+1)
	for i := range many {
		many[i] = Word{StartMs: 0, EndMs: 1, Text: "a"}
	}
	for name, words := range map[string][]Word{
		"blank":     {{StartMs: 0, EndMs: 1, Text: "  "}},
		"too many":  many,
		"negative":  {{StartMs: -1, EndMs: 1, Text: "a"}},
		"backwards": {{StartMs: 5, EndMs: 1, Text: "a"}},
	} {
		if _, err := Normalize([]Cue{{StartMs: 0, EndMs: 10, Text: "a", Words: words}}); !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	got, err := Normalize([]Cue{{StartMs: 0, EndMs: 10, Text: "a b", Words: []Word{{StartMs: 5, EndMs: 9, Text: " b"}, {StartMs: 0, EndMs: 4, Text: "a "}}}})
	if err != nil || !reflect.DeepEqual(got[0].Words, []Word{{StartMs: 0, EndMs: 4, Text: "a"}, {StartMs: 5, EndMs: 9, Text: "b"}}) {
		t.Fatalf("sorted = %+v, %v", got, err)
	}
}

func TestRenderJSONRoundTrip(t *testing.T) {
	cues, err := Parse(JSON, whisper)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(JSON, cues)
	if err != nil || !strings.Contains(out, `"start":0.12,"end":0.5,"confidence":0.93`) || strings.Contains(out, "probability") {
		t.Fatalf("json = %s, %v", out, err)
	}
	back, err := Parse(JSON, out)
	if err != nil || !reflect.DeepEqual(back, cues) {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
	if out, _ := Render(SRT, cues); strings.Contains(out, "0.12") {
		t.Fatalf("srt carries words: %s", out)
	}
}

func TestWordsColumn(t *testing.T) {
	if encodeWords(nil) != nil {
		t.Fatal("no words must be NULL")
	}
	if words, err := DecodeWords(nil); words != nil || err != nil {
		t.Fatalf("NULL = %v, %v", words, err)
	}
	words := []Word{{StartMs: 1, EndMs: 2, Text: "a", Confidence: ptr(0.25)}, {StartMs: 3, EndMs: 4, Text: "b"}}
	raw := encodeWords(words)
	if string(raw) != `[{"startMs":1,"endMs":2,"text":"a","confidence":0.25},{"startMs":3,"endMs":4,"text":"b"}]` {
		t.Fatalf("raw = %s", raw)
	}
	if back, err := DecodeWords(raw); err != nil || !reflect.DeepEqual(back, words) {
		t.Fatalf("decoded = %+v, %v", back, err)
	}
	if _, err := DecodeWords([]byte(`{`)); err == nil {
		t.Fatal("broken column must fail")
	}
}
