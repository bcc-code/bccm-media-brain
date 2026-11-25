package subtitle

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/bcc-media/mediabrain/internal/domain"
)

// transcript is the Whisper JSON layout the transcriber writes (transcript.json): one segment
// per cue, times in seconds.
type transcript struct {
	Text     string    `json:"text"`
	Segments []segment `json:"segments"`
}

type segment struct {
	ID      int           `json:"id"`
	Start   float64       `json:"start"`
	End     float64       `json:"end"`
	Text    string        `json:"text"`
	Speaker *string       `json:"speaker,omitempty"`
	Words   []segmentWord `json:"words,omitempty"`
}

// segmentWord also reads openai-whisper's spelling, "word" and "probability".
type segmentWord struct {
	Text        string   `json:"text"`
	Word        string   `json:"word,omitempty"`
	Start       float64  `json:"start"`
	End         float64  `json:"end"`
	Confidence  *float64 `json:"confidence,omitempty"`
	Probability *float64 `json:"probability,omitempty"`
}

func toMillis(seconds float64) (int32, bool) {
	ms := math.Round(seconds * 1000)
	return int32(ms), ms >= 0 && ms <= maxMs
}

func seconds(ms int32) float64 { return float64(ms) / 1000 }

func parseJSON(content string) ([]Cue, error) {
	var t transcript
	if err := json.Unmarshal([]byte(strings.TrimPrefix(content, "\ufeff")), &t); err != nil {
		return nil, domain.Invalid("not a JSON transcript: %s", err)
	}
	cues := make([]Cue, 0, len(t.Segments))
	for _, s := range t.Segments {
		text := strings.TrimSpace(s.Text)
		if text == "" {
			continue
		}
		n := len(cues) + 1
		start, okStart := toMillis(s.Start)
		end, okEnd := toMillis(s.End)
		if !okStart || !okEnd {
			return nil, domain.Invalid("cue %d: time out of range", n)
		}
		cue := Cue{StartMs: start, EndMs: end, Text: text, Speaker: s.Speaker}
		for _, w := range s.Words {
			text := w.Text
			if text == "" {
				text = w.Word
			}
			if strings.TrimSpace(text) == "" {
				continue
			}
			ws, okStart := toMillis(w.Start)
			we, okEnd := toMillis(w.End)
			if !okStart || !okEnd {
				return nil, domain.Invalid("cue %d: word time out of range", n)
			}
			confidence := w.Confidence
			if confidence == nil {
				confidence = w.Probability
			}
			cue.Words = append(cue.Words, Word{StartMs: ws, EndMs: we, Text: text, Confidence: confidence})
		}
		cues = append(cues, cue)
	}
	return Normalize(cues)
}

func renderJSON(cues []Cue) (string, error) {
	t := transcript{Segments: make([]segment, len(cues))}
	texts := make([]string, len(cues))
	for i, c := range cues {
		s := segment{ID: i, Start: seconds(c.StartMs), End: seconds(c.EndMs), Text: c.Text, Speaker: c.Speaker}
		for _, w := range c.Words {
			s.Words = append(s.Words, segmentWord{Text: w.Text, Start: seconds(w.StartMs), End: seconds(w.EndMs), Confidence: w.Confidence})
		}
		t.Segments[i], texts[i] = s, c.Text
	}
	t.Text = strings.Join(texts, " ")
	b, err := json.Marshal(t)
	return string(b), err
}

// encodeWords gives nil, stored as NULL, for a cue without word timing.
func encodeWords(words []Word) json.RawMessage {
	if len(words) == 0 {
		return nil
	}
	b, _ := json.Marshal(words)
	return b
}

// DecodeWords reads the words column; NULL gives nil.
func DecodeWords(raw json.RawMessage) ([]Word, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var words []Word
	err := json.Unmarshal(raw, &words)
	return words, err
}
