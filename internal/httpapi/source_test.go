package httpapi

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"
)

func TestProjectFromSourceAndSync(t *testing.T) {
	e, wt := fullEnv(t)
	c := &client{e: e}
	c.login(t)

	w := c.do(t, http.MethodPost, basePath+"/admin/repository-preview", map[string]string{"path": wt}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("preview %d %s", w.Code, w.Body)
	}
	if got := decodeBody[struct{ Branches []string }](t, w.Body.Bytes()); !slices.Equal(got.Branches, []string{"fix", "main"}) {
		t.Fatalf("branches %v", got.Branches)
	}
	if w := c.do(t, http.MethodPost, basePath+"/admin/repository-preview", map[string]string{"path": t.TempDir()}, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("preview of a plain folder %d", w.Code)
	}

	w = c.do(t, http.MethodPost, basePath+"/projects", map[string]string{"name": "fromsrc", "description": "", "source_path": wt}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	if p := decodeBody[projectJSON](t, w.Body.Bytes()); p.SourcePath != wt {
		t.Fatalf("project %+v", p)
	}
	w = c.do(t, http.MethodPost, basePath+"/projects/fromsrc/reviews",
		map[string]string{"source_branch": "refs/heads/fix", "target_branch": "refs/heads/main", "title": "Fix"}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("open review %d %s", w.Code, w.Body)
	}

	// A new commit in the source becomes revision 2 after a sync.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "commit", "-q", "--allow-empty", "-m", "third")
	cmd.Dir = wt
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + wt, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=Ann", "GIT_AUTHOR_EMAIL=ann@example.invalid", "GIT_COMMITTER_NAME=Ann", "GIT_COMMITTER_EMAIL=ann@example.invalid"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}
	w = c.do(t, http.MethodPost, basePath+"/projects/fromsrc/sync", map[string]bool{"overwrite": false}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("sync %d %s", w.Code, w.Body)
	}
	res := decodeBody[struct {
		Created, Updated, Rewritten, Unchanged []string
	}](t, w.Body.Bytes())
	if !slices.Equal(res.Updated, []string{"fix"}) || !slices.Equal(res.Unchanged, []string{"main"}) || res.Created == nil {
		t.Fatalf("sync result %+v", res)
	}
	w = c.do(t, http.MethodGet, basePath+"/projects/fromsrc/reviews/1", nil, nil)
	detail := decodeBody[struct {
		Revisions []struct{ Number int } `json:"revisions"`
	}](t, w.Body.Bytes())
	if len(detail.Revisions) != 2 {
		t.Fatalf("revisions after sync: %+v", detail.Revisions)
	}

	if w := c.do(t, http.MethodPost, basePath+"/projects/libfoo/sync", map[string]bool{}, nil); w.Code != http.StatusConflict {
		t.Fatalf("sync without a source %d", w.Code)
	}
}
