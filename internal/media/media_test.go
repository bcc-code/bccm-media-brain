package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/pkg/vidispine"
)

type fakePreviewer struct {
	st             *vidispine.FileStream
	err            error
	id, rangeAsked string
}

func (f *fakePreviewer) Preview(_ context.Context, id, rangeHeader string) (*vidispine.FileStream, error) {
	f.id, f.rangeAsked = id, rangeHeader
	return f.st, f.err
}

type closer struct {
	io.Reader
	closed bool
}

func (c *closer) Close() error { c.closed = true; return nil }

func serve(p Previewer, method string, header map[string]string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.Handle("GET /media/renditions/{id}", Handler(p))
	req := httptest.NewRequest(method, "/media/renditions/RE-1", nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHandlerStreams(t *testing.T) {
	body := &closer{Reader: strings.NewReader("234")}
	p := &fakePreviewer{st: &vidispine.FileStream{
		Status: http.StatusPartialContent, Body: body,
		Header: http.Header{"Content-Type": {"video/mp4"}, "Content-Range": {"bytes 2-4/10"}, "Content-Length": {"3"}},
	}}
	rec := serve(p, http.MethodGet, map[string]string{"Range": "bytes=2-4"})
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "234" || !body.closed {
		t.Fatalf("= %d %q, closed %v", rec.Code, rec.Body, body.closed)
	}
	if p.id != "RE-1" || p.rangeAsked != "bytes=2-4" {
		t.Fatalf("asked for %q %q", p.id, p.rangeAsked)
	}
	h := rec.Header()
	if h.Get("Content-Type") != "video/mp4" || h.Get("Content-Range") != "bytes 2-4/10" || h.Get("Cache-Control") != "private, max-age=300" {
		t.Fatalf("headers = %v", h)
	}

	p.st = &vidispine.FileStream{Status: http.StatusOK, Header: http.Header{}, Body: &closer{Reader: strings.NewReader("all")}}
	if rec := serve(p, http.MethodHead, nil); rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD = %d %q", rec.Code, rec.Body)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// A stream that breaks off has already sent its status; the handler only logs it.
func TestHandlerStreamEndsEarly(t *testing.T) {
	p := &fakePreviewer{st: &vidispine.FileStream{Status: http.StatusOK, Header: http.Header{}, Body: &closer{Reader: failingReader{}}}}
	if rec := serve(p, http.MethodGet, nil); rec.Code != http.StatusOK {
		t.Fatalf("= %d", rec.Code)
	}
}

func TestHandlerErrors(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
	}{
		{domain.ErrNotFound, http.StatusNotFound},
		{domain.ErrForbidden, http.StatusNotFound},
		{domain.ErrUnauthenticated, http.StatusUnauthorized},
		{domain.Unavailable("Vidispine did not answer"), http.StatusServiceUnavailable},
		{context.Canceled, http.StatusServiceUnavailable},
		{fmt.Errorf("db: %w", errors.New("connection refused")), http.StatusInternalServerError},
	} {
		rec := serve(&fakePreviewer{err: c.err}, http.MethodGet, nil)
		if rec.Code != c.status {
			t.Errorf("%v = %d, want %d", c.err, rec.Code, c.status)
		}
		// Nothing about the cause reaches the caller.
		if strings.Contains(rec.Body.String(), "connection") || strings.Contains(rec.Body.String(), "Vidispine") {
			t.Errorf("%v leaked: %q", c.err, rec.Body)
		}
	}
}

func TestVidispineFileID(t *testing.T) {
	for raw, want := range map[string]string{
		`{"vidispine": {"shapeId": "VX-1", "fileId": "VX-2"}}`: "VX-2",
		`{"vidispine": {"shapeId": "VX-1"}}`:                   "",
		`{"other": {"fileId": "VX-2"}}`:                        "",
		`{"vidispine": "VX-2"}`:                                "",
		`{}`:                                                   "",
	} {
		if got := vidispineFileID([]byte(raw)); got != want {
			t.Errorf("%s = %q, want %q", raw, got, want)
		}
	}
}
