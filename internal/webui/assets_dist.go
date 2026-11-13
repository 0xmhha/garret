//go:build webui

package webui

import (
	"embed"
	"io/fs"
)

// dist is written by `make web` (vite build). Building with the webui tag
// without it fails to compile, which is intended: a tagged build must carry
// the real interface.
//
//go:embed all:dist
var embedded embed.FS

// Files returns the embedded Vite build.
func Files() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		// Unreachable: the directory is embedded above. Serving the
		// whole embed keeps the server up instead of panicking.
		return embedded
	}
	return sub
}

// Built reports whether the real interface is embedded.
func Built() bool { return true }
