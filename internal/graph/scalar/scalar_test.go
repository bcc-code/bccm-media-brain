package scalar

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bcc-media/mediabrain/internal/domain"
)

func TestDate(t *testing.T) {
	var b bytes.Buffer
	MarshalDate(time.Date(2026, 3, 9, 15, 0, 0, 0, time.UTC)).MarshalGQL(&b)
	if b.String() != `"2026-03-09"` {
		t.Fatalf("marshal = %s", b.String())
	}
	got, err := UnmarshalDate("2026-03-09")
	if err != nil || !got.Equal(time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unmarshal = %v, %v", got, err)
	}
	for _, bad := range []any{"09.03.2026", 20260309, nil} {
		if _, err := UnmarshalDate(bad); !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("%v: expected invalid input, got %v", bad, err)
		}
	}
}

func TestJSON(t *testing.T) {
	var b bytes.Buffer
	MarshalJSON(json.RawMessage(`{"a":1}`)).MarshalGQL(&b)
	MarshalJSON(nil).MarshalGQL(&b)
	if b.String() != `{"a":1}null` {
		t.Fatalf("marshal = %s", b.String())
	}
	got, err := UnmarshalJSON(map[string]any{"a": []any{1.0, "x"}})
	if err != nil || string(got) != `{"a":[1,"x"]}` {
		t.Fatalf("unmarshal = %s, %v", got, err)
	}
}

func TestTime(t *testing.T) {
	at := time.Date(2026, 3, 9, 15, 4, 5, 0, time.UTC)
	var b bytes.Buffer
	MarshalTime(at).MarshalGQL(&b)
	if b.String() != `"2026-03-09T15:04:05Z"` {
		t.Fatalf("marshal = %s", b.String())
	}
	if got, err := UnmarshalTime("2026-03-09T15:04:05Z"); err != nil || !got.Equal(at) {
		t.Fatalf("unmarshal = %v, %v", got, err)
	}
	for _, bad := range []any{"tomorrow", 20260309} {
		if _, err := UnmarshalTime(bad); !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("%v: expected invalid input, got %v", bad, err)
		}
	}
}
