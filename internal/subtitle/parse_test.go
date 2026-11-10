package subtitle

import (
	"errors"
	"strings"
	"testing"

	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
)

const srt = "\ufeff1\r\n00:00:01,000 --> 00:00:02,500\r\nHello\r\nworld\r\n\r\n" +
	"2\r\n00:00:03,000 --> 00:00:04,000\r\nSecond\r\n\r\n\r\n"

const vtt = `WEBVTT - a title

NOTE this is a comment
spanning lines

STYLE
::cue { color: red }

intro
00:01.000 --> 00:02.500 align:start position:10%
<v Anna>Hello
world</v>

1:00:03.000 --> 1:00:04.000
Second
`

func TestParse(t *testing.T) {
	cues, err := Parse(SRT, srt)
	if err != nil || len(cues) != 2 {
		t.Fatalf("srt: %+v, %v", cues, err)
	}
	if c := cues[0]; c.StartMs != 1000 || c.EndMs != 2500 || c.Text != "Hello\nworld" || c.Speaker != nil {
		t.Fatalf("srt cue 1 = %+v", c)
	}

	cues, err = Parse(VTT, vtt)
	if err != nil || len(cues) != 2 {
		t.Fatalf("vtt: %+v, %v", cues, err)
	}
	if c := cues[0]; c.StartMs != 1000 || c.EndMs != 2500 || c.Text != "Hello\nworld" || c.Speaker == nil || *c.Speaker != "Anna" {
		t.Fatalf("vtt cue 1 = %+v", c)
	}
	if c := cues[1]; c.StartMs != 3603000 || c.EndMs != 3604000 {
		t.Fatalf("vtt cue 2 = %+v", c)
	}
}

func TestParseOrdersAndSkipsEmpty(t *testing.T) {
	in := "00:00:05,000 --> 00:00:06,000\nlater\n\n00:00:01,000 --> 00:00:02,000\nearlier\n\n00:00:07,000 --> 00:00:08,000\n\n"
	cues, err := Parse(SRT, in)
	if err != nil || len(cues) != 2 || cues[0].Text != "earlier" || cues[1].Text != "later" {
		t.Fatalf("%+v, %v", cues, err)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]struct {
		format  Format
		content string
	}{
		"unknown format": {"ass", srt},
		"empty":          {SRT, "  \n\n "},
		"header only":    {VTT, "WEBVTT\n\n"},
		"no timing":      {SRT, "1\nnot a timing line that is long enough to be clipped in the message\ntext\n"},
		"end before":     {SRT, "00:00:02,000 --> 00:00:01,000\nx\n"},
		"zero length":    {SRT, "00:00:02,000 --> 00:00:02,000\nx\n"},
		"bad minutes":    {SRT, "00:61:00,000 --> 00:62:00,000\nx\n"},
		"too far":        {SRT, "900:00:00,000 --> 901:00:00,000\nx\n"},
		"too large":      {SRT, strings.Repeat("x", MaxContentBytes+1)},
		"too many cues":  {SRT, strings.Repeat("00:00:01,000 --> 00:00:02,000\nx\n\n", MaxCues+1)},
	}
	for name, c := range cases {
		if _, err := Parse(c.format, c.content); !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestNormalize(t *testing.T) {
	blank := " "
	cues, err := Normalize([]Cue{{StartMs: 10, EndMs: 20, Text: " b ", Speaker: &blank}, {StartMs: 0, EndMs: 5, Text: "a"}})
	if err != nil || cues[0].Text != "a" || cues[1].Text != "b" || cues[1].Speaker != nil {
		t.Fatalf("%+v, %v", cues, err)
	}
	for name, c := range map[string]Cue{
		"no text":  {StartMs: 0, EndMs: 1, Text: " "},
		"negative": {StartMs: -1, EndMs: 1, Text: "x"},
		"reversed": {StartMs: 5, EndMs: 5, Text: "x"},
	} {
		if _, err := Normalize([]Cue{c}); !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestRenderRoundTrip(t *testing.T) {
	anna := "Anna"
	cues := []Cue{
		{StartMs: 1000, EndMs: 2500, Text: "Hello\nworld", Speaker: &anna},
		{StartMs: 3723004, EndMs: 3724000, Text: "Second"},
	}
	out, err := Render(VTT, cues)
	if err != nil || !strings.HasPrefix(out, "WEBVTT\n\n00:00:01.000 --> 00:00:02.500\n<v Anna>Hello\nworld\n\n01:02:03.004 --> ") {
		t.Fatalf("vtt = %q, %v", out, err)
	}
	back, err := Parse(VTT, out)
	if err != nil || len(back) != 2 || *back[0].Speaker != "Anna" || back[0].Text != cues[0].Text || back[1] != cues[1] {
		t.Fatalf("vtt round trip = %+v, %v", back, err)
	}

	out, err = Render(SRT, cues)
	if err != nil || !strings.HasPrefix(out, "1\n00:00:01,000 --> 00:00:02,500\nHello\nworld\n\n2\n") {
		t.Fatalf("srt = %q, %v", out, err)
	}
	back, err = Parse(SRT, out)
	if err != nil || len(back) != 2 || back[0].Speaker != nil || back[1] != cues[1] {
		t.Fatalf("srt round trip = %+v, %v", back, err)
	}
	if _, err := Render("ass", cues); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("unknown format: %v", err)
	}
}

func TestLatestOnly(t *testing.T) {
	tracks := []sqlc.SubtitleTrack{
		{RenditionID: "RE-a", Revision: 2},
		{RenditionID: "RE-a", Revision: 1},
		{RenditionID: "RE-b", Revision: 1},
	}
	if got := latestOnly(tracks, true); len(got) != 3 {
		t.Fatalf("history = %d", len(got))
	}
	got := latestOnly(tracks, false)
	if len(got) != 2 || got[0].Revision != 2 || got[1].RenditionID != "RE-b" {
		t.Fatalf("latest = %+v", got)
	}
}
