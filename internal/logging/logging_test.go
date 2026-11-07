package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	var buf bytes.Buffer
	l, err := New(&buf, "json", slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	l.Debug("hidden")
	l.Info("hello", "k", 1)
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil || m["msg"] != "hello" || m["k"] != float64(1) {
		t.Fatalf("json output %q: %v", buf.String(), err)
	}

	buf.Reset()
	for _, f := range []string{"", "TEXT"} {
		l, err := New(&buf, f, slog.LevelDebug)
		if err != nil {
			t.Fatal(err)
		}
		l.Debug("dbg")
	}
	if strings.Count(buf.String(), "msg=dbg") != 2 {
		t.Fatalf("text output %q", buf.String())
	}

	if _, err := New(&buf, "xml", slog.LevelInfo); err == nil {
		t.Fatal("expected error for unknown format")
	}
}
