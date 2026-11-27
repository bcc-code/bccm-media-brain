package media

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bcc-media/mediabrain/internal/domain"
)

type fakePreviewer struct {
	path, mime string
	err        error
	id         string
}

func (f *fakePreviewer) Preview(_ context.Context, id string) (*File, error) {
	f.id = id
	if f.err != nil {
		return nil, f.err
	}
	o, err := open(f.path)
	if err != nil {
		return nil, err
	}
	return &File{File: o.file, Name: filepath.Base(f.path), ModTime: o.modTime, MimeType: f.mime}, nil
}

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

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHandlerServesRanges(t *testing.T) {
	p := &fakePreviewer{path: writeFile(t, "preview.mp4", "0123456789"), mime: "video/mp4"}
	rec := serve(p, http.MethodGet, map[string]string{"Range": "bytes=2-4"})
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "234" || p.id != "RE-1" {
		t.Fatalf("range = %d %q for %q", rec.Code, rec.Body, p.id)
	}
	h := rec.Header()
	if h.Get("Content-Type") != "video/mp4" || h.Get("Content-Range") != "bytes 2-4/10" || h.Get("Cache-Control") != "private, max-age=300" {
		t.Fatalf("headers = %v", h)
	}
	if rec := serve(p, http.MethodGet, nil); rec.Code != http.StatusOK || rec.Body.String() != "0123456789" || rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("whole = %d %q %v", rec.Code, rec.Body, rec.Header())
	}
	if rec := serve(p, http.MethodHead, nil); rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD = %d %q", rec.Code, rec.Body)
	}
	// Without a registered type, the file's name says it.
	p.mime = ""
	if rec := serve(p, http.MethodGet, nil); rec.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("type from the name = %q", rec.Header().Get("Content-Type"))
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
		{domain.Unavailable("the preview is on no storage this server can read"), http.StatusServiceUnavailable},
		{fmt.Errorf("db: %w", errors.New("connection refused")), http.StatusInternalServerError},
	} {
		rec := serve(&fakePreviewer{err: c.err}, http.MethodGet, nil)
		if rec.Code != c.status {
			t.Errorf("%v = %d, want %d", c.err, rec.Code, c.status)
		}
		// Nothing about the cause reaches the caller.
		if strings.Contains(rec.Body.String(), "connection") || strings.Contains(rec.Body.String(), "storage") {
			t.Errorf("%v leaked: %q", c.err, rec.Body)
		}
	}
}

func TestLocalPath(t *testing.T) {
	for _, c := range []struct {
		base, rel, want string
	}{
		{"file:///mnt/VX-42/", "raw/a_lowres_wm.mp4", "/mnt/VX-42/raw/a_lowres_wm.mp4"},
		{"file:///mnt/VX-42", "a.mp4", "/mnt/VX-42/a.mp4"},
		{"file://localhost/mnt/x/", "a b.mp4", "/mnt/x/a b.mp4"},
		{"file:///mnt/a%20b/", "c.mp4", "/mnt/a b/c.mp4"},
		{"file:///mnt/VX-42/", "../other/a.mp4", ""},
		{"file:///mnt/VX-42/", "raw/../../a.mp4", ""},
		{"file:///mnt/VX-42/", "", ""},
		{"file://nas.example/mnt/x/", "a.mp4", ""},
		{"s3://bucket/", "a.mp4", ""},
		{"file:", "a.mp4", ""},
		{"::", "a.mp4", ""},
	} {
		got, ok := localPath(c.base, c.rel)
		if ok != (c.want != "") || got != filepath.FromSlash(c.want) {
			t.Errorf("localPath(%q, %q) = %q %v, want %q", c.base, c.rel, got, ok, c.want)
		}
	}
}

func TestOpenRefusesWhatIsNotAFile(t *testing.T) {
	if _, err := open(t.TempDir()); err == nil {
		t.Fatal("a directory was opened")
	}
	if _, err := open(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing file was opened")
	}
	f, err := open(writeFile(t, "a.mp4", "x"))
	if err != nil || f.modTime.After(time.Now()) {
		t.Fatalf("open = %v, %v", f, err)
	}
	_ = f.file.Close()
}
