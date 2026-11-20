//go:build webui

package webui

import (
	"embed"
	"io/fs"
)

// dist is a copy of web/.output/public, put here by `make web-build` or the Dockerfile.
//
//go:embed all:dist
var dist embed.FS

// Files returns the embedded build.
func Files() fs.FS {
	files, _ := fs.Sub(dist, "dist") // the embedded directory; cannot fail
	return files
}
