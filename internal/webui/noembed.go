//go:build !webui

package webui

import "io/fs"

// Files returns nil: this binary was built without the web UI, so that building and testing the
// Go code needs no Node. `make build` and the image build with the `webui` tag.
func Files() fs.FS { return nil }
