package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// An export that fails before any byte of the archive is written answers
// with an error, not with 200 and an empty archive the browser cannot read.
func TestExportFailureBeforeArchiveIsAnError(t *testing.T) {
	e, wt := fullEnv(t)
	c := &client{e: e}
	c.login(t)
	if err := os.RemoveAll(filepath.Join(filepath.Dir(wt), "repos", "libfoo.git")); err != nil {
		t.Fatal(err)
	}
	w := c.do(t, http.MethodPost, basePath+"/admin/projects/libfoo/export", nil, nil)
	if w.Code == http.StatusOK {
		t.Fatalf("export of a broken repository answered 200 with %d bytes", w.Body.Len())
	}
	if got := w.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("error carries an attachment header %q", got)
	}
	if errorCode(t, w) == "" {
		t.Fatalf("no error body: %s", w.Body)
	}
}
