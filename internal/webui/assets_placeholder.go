//go:build !webui

package webui

import (
	"embed"
	"io/fs"
)

//go:embed placeholder
var embedded embed.FS

// Files returns the embedded interface: here the placeholder page, because
// the binary was built without the webui tag.
func Files() fs.FS {
	sub, err := fs.Sub(embedded, "placeholder")
	if err != nil {
		// Unreachable: the directory is embedded above. Serving the
		// whole embed keeps the server up instead of panicking.
		return embedded
	}
	return sub
}

// Built reports whether the real interface is embedded.
func Built() bool { return false }
