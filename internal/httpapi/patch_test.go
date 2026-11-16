package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestReviewPatch(t *testing.T) {
	e, _ := fullEnv(t)
	c := &client{e: e}
	c.login(t)
	p := basePath + "/projects/libfoo/reviews"
	if w := c.do(t, http.MethodPost, p, map[string]string{"source_branch": "refs/heads/fix", "target_branch": "refs/heads/main", "title": "Fix"}, nil); w.Code != http.StatusCreated {
		t.Fatalf("open %d %s", w.Code, w.Body)
	}
	w := c.do(t, http.MethodGet, p+"/1/patch?rev=1", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("patch %d %s", w.Code, w.Body)
	}
	h := w.Header()
	if h.Get("Content-Disposition") != `attachment; filename=libfoo-1-r1.patch` {
		t.Fatalf("disposition %q", h.Get("Content-Disposition"))
	}
	if h.Get("Content-Type") != "application/octet-stream" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("served as %q with CSP %q", h.Get("Content-Type"), h.Get("Content-Security-Policy"))
	}
	if n := strings.Count(w.Body.String(), "\nSubject: [PATCH"); n != 2 {
		t.Fatalf("%d patches in\n%s", n, w.Body)
	}
	if w := c.do(t, http.MethodGet, p+"/1/patch?from=1&to=1", nil, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("revision pair %d", w.Code)
	}
	if w := c.do(t, http.MethodGet, p+"/9/patch?rev=1", nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("unknown review %d", w.Code)
	}
}
