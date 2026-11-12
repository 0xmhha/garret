package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testFiles() fstest.MapFS {
	return fstest.MapFS{
		"index.html":            {Data: []byte("<!doctype html><title>garret</title>")},
		"assets/app-3f9a1c.js":  {Data: []byte("console.log(1)")},
		"assets/app-3f9a1c.css": {Data: []byte("body{}")},
		"favicon.svg":           {Data: []byte("<svg/>")},
	}
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	return w
}

func TestHandler(t *testing.T) {
	h := Handler(testFiles())
	tests := []struct {
		path, body, cache, ctype string
		code                     int
	}{
		{"/", "<title>garret</title>", "no-store", "text/html", http.StatusOK},
		{"/p/libfoo/r/12/files", "<title>garret</title>", "no-store", "text/html", http.StatusOK},
		{"/index.html", "<title>garret</title>", "no-store", "text/html", http.StatusOK},
		{"/assets/app-3f9a1c.js", "console.log(1)", "public, max-age=31536000, immutable", "javascript", http.StatusOK},
		{"/assets/app-3f9a1c.css", "body{}", "public, max-age=31536000, immutable", "text/css", http.StatusOK},
		{"/favicon.svg", "<svg/>", "no-cache", "image/svg+xml", http.StatusOK},
		{"/assets/missing-000000.js", "", "", "", http.StatusNotFound},
		{"/assets/../index.html", "<title>garret</title>", "no-store", "text/html", http.StatusOK},
	}
	for _, tt := range tests {
		w := get(t, h, tt.path)
		if w.Code != tt.code {
			t.Errorf("%s: status %d, want %d", tt.path, w.Code, tt.code)
			continue
		}
		if tt.code != http.StatusOK {
			continue
		}
		if !strings.Contains(w.Body.String(), tt.body) || w.Header().Get("Cache-Control") != tt.cache || !strings.Contains(w.Header().Get("Content-Type"), tt.ctype) {
			t.Errorf("%s: body %q cache %q type %q", tt.path, w.Body.String(), w.Header().Get("Cache-Control"), w.Header().Get("Content-Type"))
		}
		for _, hdr := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
			if w.Header().Get(hdr) == "" {
				t.Errorf("%s: missing %s", tt.path, hdr)
			}
		}
	}
}

func TestHandlerRejectsOtherMethods(t *testing.T) {
	w := httptest.NewRecorder()
	Handler(testFiles()).ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status %d", w.Code)
	}
}

func TestEmbeddedFilesServe(t *testing.T) {
	// Whichever build is embedded, it has an index page the handler serves.
	if w := get(t, Handler(Files()), "/"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "garret") {
		t.Fatalf("embedded index %d %s", w.Code, w.Body.String())
	}
	if Built() == strings.Contains(get(t, Handler(Files()), "/").Body.String(), "not part of this build") {
		t.Fatal("Built() disagrees with the embedded page")
	}
}
