package subtitle

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/bcc-media/mediabrain/internal/domain"
)

type Format string

const (
	SRT  Format = "srt"
	VTT  Format = "vtt"
	JSON Format = "json"
)

const (
	MaxContentBytes = 5 << 20
	MaxCues         = 50000
	MaxWordsPerCue  = 1000
	maxMs           = 1<<31 - 1
)

type Cue struct {
	StartMs int32
	EndMs   int32
	Text    string
	Speaker *string
	// Words is the word timing speech recognition reported, if any. It is kept as given: words
	// may overhang the cue (Whisper's often do) and need not spell out Text.
	Words []Word
}

type Word struct {
	StartMs    int32    `json:"startMs"`
	EndMs      int32    `json:"endMs"`
	Text       string   `json:"text"`
	Confidence *float64 `json:"confidence,omitempty"`
}

var (
	// Both formats, either decimal separator, hours optional; anything after the end time
	// (WebVTT cue settings) is ignored.
	timing   = regexp.MustCompile(`^\s*(?:(\d+):)?(\d{1,2}):(\d{2})[.,](\d{3})\s+-->\s+(?:(\d+):)?(\d{1,2}):(\d{2})[.,](\d{3})(?:\s.*)?$`)
	voiceTag = regexp.MustCompile(`^<v(?:\.[^\s>]*)?\s+([^>]+)>`)
)

func toMs(h, m, s, ms string) (int64, bool) {
	hh, _ := strconv.ParseInt("0"+h, 10, 64)
	mm, _ := strconv.ParseInt(m, 10, 64)
	ss, _ := strconv.ParseInt(s, 10, 64)
	mmm, _ := strconv.ParseInt(ms, 10, 64)
	total := ((hh*60+mm)*60+ss)*1000 + mmm
	return total, mm < 60 && ss < 60 && total <= maxMs
}

// Parse reads SRT, WebVTT or a Whisper JSON transcript into cues, ordered by start time.
func Parse(format Format, content string) ([]Cue, error) {
	if format != SRT && format != VTT && format != JSON {
		return nil, domain.InvalidField("format", "format must be SRT, VTT or JSON")
	}
	if len(content) > MaxContentBytes {
		return nil, domain.Invalid("subtitle content is larger than %d MB", MaxContentBytes>>20)
	}
	if format == JSON {
		return parseJSON(content)
	}
	content = strings.TrimPrefix(content, "\ufeff")
	content = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(content)

	var cues []Cue
	for n, block := range strings.Split(content, "\n\n") {
		lines := strings.Split(strings.Trim(block, "\n"), "\n")
		if len(lines) == 1 && strings.TrimSpace(lines[0]) == "" {
			continue
		}
		if format == VTT {
			first := strings.TrimSpace(lines[0])
			if (n == 0 && strings.HasPrefix(first, "WEBVTT")) || strings.HasPrefix(first, "NOTE") ||
				first == "STYLE" || first == "REGION" {
				continue
			}
		}
		at := 0
		if !strings.Contains(lines[0], "-->") && len(lines) > 1 {
			at = 1 // the first line is a cue number or identifier
		}
		m := timing.FindStringSubmatch(lines[at])
		if m == nil {
			return nil, domain.Invalid("cue %d: expected a timing line, got %q", len(cues)+1, clip(lines[at]))
		}
		start, okStart := toMs(m[1], m[2], m[3], m[4])
		end, okEnd := toMs(m[5], m[6], m[7], m[8])
		if !okStart || !okEnd {
			return nil, domain.Invalid("cue %d: time out of range", len(cues)+1)
		}
		cue := Cue{StartMs: int32(start), EndMs: int32(end), Text: strings.TrimSpace(strings.Join(lines[at+1:], "\n"))}
		if format == VTT {
			if v := voiceTag.FindStringSubmatch(cue.Text); v != nil {
				speaker := strings.TrimSpace(v[1])
				cue.Speaker = &speaker
				cue.Text = strings.TrimSpace(strings.TrimSuffix(cue.Text[len(v[0]):], "</v>"))
			}
		}
		if cue.Text == "" {
			continue
		}
		cues = append(cues, cue)
	}
	return Normalize(cues)
}

func clip(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}

// Normalize validates cues and orders them by start time.
func Normalize(cues []Cue) ([]Cue, error) {
	if len(cues) == 0 {
		return nil, domain.Invalid("no cues")
	}
	if len(cues) > MaxCues {
		return nil, domain.Invalid("more than %d cues", MaxCues)
	}
	out := make([]Cue, len(cues))
	for i, c := range cues {
		c.Text = strings.TrimSpace(c.Text)
		c.Speaker = domain.Blank(c.Speaker)
		switch {
		case c.Text == "":
			return nil, domain.Invalid("cue %d: text is required", i+1)
		case c.StartMs < 0:
			return nil, domain.Invalid("cue %d: start must not be negative", i+1)
		case c.EndMs <= c.StartMs:
			return nil, domain.Invalid("cue %d: end must be after start", i+1)
		}
		words, err := normalizeWords(c.Words)
		if err != nil {
			return nil, domain.Invalid("cue %d: %s", i+1, err)
		}
		c.Words = words
		out[i] = c
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartMs < out[j].StartMs })
	return out, nil
}

func normalizeWords(words []Word) ([]Word, error) {
	if len(words) == 0 {
		return nil, nil
	}
	if len(words) > MaxWordsPerCue {
		return nil, fmt.Errorf("more than %d words", MaxWordsPerCue)
	}
	out := make([]Word, len(words))
	for i, w := range words {
		w.Text = strings.TrimSpace(w.Text)
		switch {
		case w.Text == "":
			return nil, fmt.Errorf("word %d: text is required", i+1)
		case w.StartMs < 0:
			return nil, fmt.Errorf("word %d: start must not be negative", i+1)
		case w.EndMs < w.StartMs:
			return nil, fmt.Errorf("word %d: end must not be before start", i+1)
		case w.Confidence != nil && !(*w.Confidence >= 0 && *w.Confidence <= 1):
			return nil, fmt.Errorf("word %d: confidence must be between 0 and 1", i+1)
		}
		out[i] = w
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartMs < out[j].StartMs })
	return out, nil
}

func stamp(ms int32, sep string) string {
	return fmt.Sprintf("%02d:%02d:%02d%s%03d", ms/3600000, ms/60000%60, ms/1000%60, sep, ms%1000)
}

// Render writes cues as SRT, WebVTT or Whisper JSON. SRT has no place for the speaker and
// only JSON keeps word timing.
func Render(format Format, cues []Cue) (string, error) {
	var b strings.Builder
	switch format {
	case SRT:
		for i, c := range cues {
			fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, stamp(c.StartMs, ","), stamp(c.EndMs, ","), c.Text)
		}
	case VTT:
		b.WriteString("WEBVTT\n\n")
		for _, c := range cues {
			text := c.Text
			if c.Speaker != nil {
				text = "<v " + *c.Speaker + ">" + text
			}
			fmt.Fprintf(&b, "%s --> %s\n%s\n\n", stamp(c.StartMs, "."), stamp(c.EndMs, "."), text)
		}
	case JSON:
		return renderJSON(cues)
	default:
		return "", domain.InvalidField("format", "format must be SRT, VTT or JSON")
	}
	return b.String(), nil
}
