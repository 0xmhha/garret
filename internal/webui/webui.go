package webui

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/0xmhha/garret/internal/httpapi"
)

const (
	indexFile      = "index.html"
	assetsPrefix   = "assets/"
	cacheImmutable = "public, max-age=31536000, immutable"
	cacheNoStore   = "no-store"
	cacheRevalid   = "no-cache"
)

// Handler serves files from fsys (docs/specs/web.md 4): hashed files under
// assets/ are cached for a year, other files are revalidated, and every
// path that names no file returns index.html, uncached, for the browser's
// router. A missing file under assets/ is 404, not the index. Every
// response carries the security headers.
func Handler(fsys fs.FS) http.Handler {
	return httpapi.WithSecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = indexFile
		}
		info, err := fs.Stat(fsys, name)
		isFile := err == nil && !info.IsDir()
		switch {
		case isFile && name != indexFile:
			cache := cacheRevalid
			if strings.HasPrefix(name, assetsPrefix) {
				cache = cacheImmutable
			}
			serveFile(w, r, fsys, name, cache)
		case strings.HasPrefix(name, assetsPrefix):
			http.NotFound(w, r)
		case err == nil || errors.Is(err, fs.ErrNotExist):
			serveFile(w, r, fsys, indexFile, cacheNoStore)
		default:
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
	}))
}

func serveFile(w http.ResponseWriter, r *http.Request, fsys fs.FS, name, cache string) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		http.Error(w, "web interface missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", cache)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
