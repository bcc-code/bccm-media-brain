package app

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWithDeadline(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	var bounded bool
	slow := withDeadline(20*time.Millisecond, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, bounded = r.Context().Deadline()
		<-r.Context().Done()
	}))
	start := time.Now()
	slow.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/query", nil))
	if !bounded || time.Since(start) > 5*time.Second || !strings.Contains(buf.String(), "request stopped at its deadline") {
		t.Fatalf("bounded %v after %v, log %q", bounded, time.Since(start), buf.String())
	}

	buf.Reset()
	quick := withDeadline(time.Minute, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	quick.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/query", nil))
	if buf.Len() != 0 {
		t.Fatalf("a request inside its deadline logged %q", buf.String())
	}
}

// The write deadline of /query is set through the logging wrapper, which must let
// http.ResponseController through to the connection.
func TestStatusRecorderUnwraps(t *testing.T) {
	inner := httptest.NewRecorder()
	if (&statusRecorder{ResponseWriter: inner}).Unwrap() != http.ResponseWriter(inner) {
		t.Fatal("Unwrap must return the wrapped writer")
	}
}
