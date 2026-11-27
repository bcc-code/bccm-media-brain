package vidispine_test

import (
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/bcc-media/mediabrain/pkg/vidispine"
)

func TestReadFile(t *testing.T) {
	srv, c := newServer(t)
	srt := []byte("1\n00:00:01,000 --> 00:00:02,000\nHei\n")
	srv.SetFileData("VX-77", srt)

	got, err := c.ReadFile(ctx, "VX-77", int64(len(srt)))
	if err != nil || string(got) != string(srt) {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}
	if _, err := c.ReadFile(ctx, "VX-77", int64(len(srt))-1); !errors.Is(err, vidispine.ErrTooLarge) {
		t.Fatalf("over the limit: %v", err)
	}
	if _, err := c.ReadFile(ctx, "VX-78", 100); !errors.Is(err, vidispine.ErrNotFound) {
		t.Fatalf("missing file: %v", err)
	}
	srv.Fail(http.StatusBadGateway)
	if _, err := c.ReadFile(ctx, "VX-77", 100); err == nil {
		t.Fatal("a failing server read fine")
	}
}

func TestReadFileRequest(t *testing.T) {
	var path, accept string
	c := clientFor(t, vidispine.Config{Retries: -1}, func(w http.ResponseWriter, r *http.Request) {
		path, accept = r.URL.EscapedPath(), r.Header.Get("Accept")
		_, _ = w.Write([]byte("x"))
	})
	if _, err := c.ReadFile(ctx, "VX-1/../x", 10); err != nil {
		t.Fatal(err)
	}
	if path != "/API/storage/file/VX-1%2F..%2Fx/data" || accept != "*/*" {
		t.Fatalf("path %q, accept %q", path, accept)
	}
}

// Vidispine names itself by the address it was set up with in a redirect. The client stays on
// the configured host, and stops a redirect loop.
func TestRedirectsStayOnTheConfiguredHost(t *testing.T) {
	var hits []string
	hc := &http.Client{}
	c := clientFor(t, vidispine.Config{Retries: -1, HTTPClient: hc, Username: "u", Password: "p"}, func(w http.ResponseWriter, r *http.Request) {
		user, _, _ := r.BasicAuth()
		hits = append(hits, r.URL.Path+" "+user)
		switch r.URL.Path {
		case "/API/storage/file/VX-1/data":
			http.Redirect(w, r, "http://localhost:1/APInoauth/x", http.StatusSeeOther)
		case "/APInoauth/x":
			_, _ = w.Write([]byte("content"))
		default:
			http.Redirect(w, r, "http://elsewhere.invalid/API/loop", http.StatusFound)
		}
	})
	got, err := c.ReadFile(ctx, "VX-1", 100)
	if err != nil || string(got) != "content" {
		t.Fatalf("ReadFile = %q, %v (requests %v)", got, err, hits)
	}
	if hits[1] != "/APInoauth/x u" {
		t.Fatalf("redirect = %q", hits[1])
	}
	if hc.CheckRedirect != nil {
		t.Fatal("the caller's http.Client was changed")
	}
	if _, err := c.ReadFile(ctx, "VX-2", 100); err == nil {
		t.Fatal("a redirect loop ended without an error")
	}
}

func TestOpenFile(t *testing.T) {
	srv, c := newServer(t)
	srv.SetFileData("VX-90", []byte("0123456789"))

	read := func(rangeHeader string) (*vidispine.FileStream, string) {
		t.Helper()
		st, err := c.OpenFile(ctx, "VX-90", rangeHeader)
		if err != nil {
			t.Fatalf("OpenFile(%q): %v", rangeHeader, err)
		}
		defer st.Body.Close()
		body, _ := io.ReadAll(st.Body)
		return st, string(body)
	}

	st, body := read("")
	if st.Status != http.StatusOK || body != "0123456789" || st.Header.Get("Content-Length") != "10" || st.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("whole file = %d %q %v", st.Status, body, st.Header)
	}
	// The range survives the redirect to the one-time URL.
	st, body = read("bytes=2-4")
	if st.Status != http.StatusPartialContent || body != "234" || st.Header.Get("Content-Range") != "bytes 2-4/10" {
		t.Fatalf("range = %d %q %v", st.Status, body, st.Header)
	}
	if st.Header.Get("Set-Cookie") != "" || st.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("headers = %v", st.Header)
	}
	st, _ = read("bytes=20-")
	if st.Status != http.StatusRequestedRangeNotSatisfiable || st.Header.Get("Content-Range") != "bytes */10" {
		t.Fatalf("range past the end = %d %v", st.Status, st.Header)
	}

	if _, err := c.OpenFile(ctx, "VX-91", ""); !errors.Is(err, vidispine.ErrNotFound) {
		t.Fatalf("missing file: %v", err)
	}
	srv.Fail(http.StatusBadGateway)
	if _, err := c.OpenFile(ctx, "VX-90", ""); err == nil {
		t.Fatal("a failing server opened the file")
	}
}

// A stream outlives the client's timeout for whole requests: only its context ends it.
func TestOpenFileHasNoTimeout(t *testing.T) {
	release := make(chan struct{})
	c := clientFor(t, vidispine.Config{HTTPClient: &http.Client{Timeout: 50 * time.Millisecond}}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("a"))
		w.(http.Flusher).Flush()
		<-release
		_, _ = w.Write([]byte("b"))
	})
	st, err := c.OpenFile(ctx, "VX-1", "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Body.Close()
	time.AfterFunc(150*time.Millisecond, func() { close(release) })
	body, err := io.ReadAll(st.Body)
	if err != nil || string(body) != "ab" {
		t.Fatalf("body = %q, %v", body, err)
	}

	bad := clientFor(t, vidispine.Config{}, func(http.ResponseWriter, *http.Request) {})
	if _, err := bad.OpenFile(nil, "VX-1", ""); err == nil { //nolint:staticcheck // a nil context is the error
		t.Fatal("a request without a context was sent")
	}
}
