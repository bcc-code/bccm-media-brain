// Package webui serves the web UI's static build (web/), which a binary built with the `webui`
// tag carries inside it.
package webui

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// assets holds what the page loads: a file that is missing must stay missing (a page in its
// place would be parsed as a script). Nuxt names these files after their content, so one that
// exists never changes, except under builds, where the page looks for a newer build.
const (
	assets = "_nuxt/"
	builds = "_nuxt/builds/"
)

// Handler serves the build in files. Mount it with http.StripPrefix. A path that is not a file
// is a route of the page itself, so it answers index.html and the router in the browser takes
// over.
func Handler(files fs.FS) http.Handler {
	static := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+req.URL.Path), "/")
		info, err := fs.Stat(files, name)
		isFile := name != "" && err == nil && !info.IsDir()
		isAsset := strings.HasPrefix(name+"/", assets)
		switch {
		case isFile && isAsset && !strings.HasPrefix(name, builds):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			static.ServeHTTP(w, req)
		case isFile:
			w.Header().Set("Cache-Control", "no-cache")
			static.ServeHTTP(w, req)
		case isAsset:
			http.NotFound(w, req)
		default:
			// index.html names the current assets, so a cached copy would keep an old build alive.
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, req, files, "index.html")
		}
	})
}
