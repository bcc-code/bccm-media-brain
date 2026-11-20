package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

var build = fstest.MapFS{
	"index.html":       {Data: []byte("<html>app</html>")},
	"favicon.ico":      {Data: []byte("icon")},
	"_nuxt/entry.a.js": {Data: []byte("console.log(1)")},

	"_nuxt/builds/latest.json":    {Data: []byte("{}")},
	"service-accounts/index.html": {Data: []byte("<html>app</html>")},
}

func get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	Handler(build).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHandler(t *testing.T) {
	for _, path := range []string{"/", "/service-accounts", "/service-accounts/", "/a/b"} {
		rec := get(t, path)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "app") {
			t.Errorf("%s = %d %q, want the page", path, rec.Code, rec.Body)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s Cache-Control = %q, want no-cache", path, got)
		}
	}

	asset := get(t, "/_nuxt/entry.a.js")
	if asset.Code != http.StatusOK || asset.Body.String() != "console.log(1)" {
		t.Fatalf("asset = %d %q", asset.Code, asset.Body)
	}
	if got := asset.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("asset Cache-Control = %q, want immutable", got)
	}

	// Files whose name does not change with their content are asked for again.
	for path, body := range map[string]string{"/favicon.ico": "icon", "/_nuxt/builds/latest.json": "{}"} {
		rec := get(t, path)
		if rec.Code != http.StatusOK || rec.Body.String() != body || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s = %d %q, Cache-Control %q", path, rec.Code, rec.Body, rec.Header().Get("Cache-Control"))
		}
	}

	// A missing asset must not answer the page, and the directory must not be listed.
	for _, path := range []string{"/_nuxt/gone.js", "/_nuxt/", "/_nuxt/../_nuxt/gone.js"} {
		if rec := get(t, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rec.Code)
		}
	}
}

func TestFilesWithoutTheTag(t *testing.T) {
	if Files() != nil {
		t.Fatal("a build without the webui tag carries no web UI")
	}
}
