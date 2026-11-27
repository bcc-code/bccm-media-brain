package vidispine_test

import (
	"errors"
	"net/http"
	"testing"

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
