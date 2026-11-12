package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/comment"
	"github.com/0xmhha/garret/internal/discussion"
	"github.com/0xmhha/garret/internal/gitrepo"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/inbox"
	"github.com/0xmhha/garret/internal/project"
	"github.com/0xmhha/garret/internal/realtime"
	"github.com/0xmhha/garret/internal/review"
)

type nopEvents struct{}

func (nopEvents) Publish(realtime.Event) {}

// fullEnv extends env with every service and a project whose repository has
// a branch fix with two commits on top of main.
func fullEnv(t *testing.T) (*env, string) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	e := newEnv(t, false)
	ctx := context.Background()
	base := t.TempDir()
	ids, _ := id.NewGenerator(wall{}, rand.Reader)
	store := e.storeDB
	rec := audit.NewRecorder(ids, wall{})
	runner, err := gitrepo.NewRunner(ctx, gitPath, 2, base)
	if err != nil {
		t.Fatal(err)
	}
	repos := filepath.Join(base, "repos")
	_ = os.Mkdir(repos, 0o700)
	projects := project.New(store, rec, ids, wall{}, runner, repos)
	notifier := inbox.NewNotifier(ids, wall{})
	e.api = New(Config{
		Auth: e.auth, Projects: projects,
		Reviews:     review.NewService(store, rec, ids, wall{}, runner, projects, nopEvents{}, notifier),
		Comments:    comment.New(store, rec, ids, wall{}, runner, projects, nopEvents{}, notifier),
		Discussions: discussion.New(store, rec, ids, wall{}, nopEvents{}),
		Origin:      origin, Active: e.active, Log: slog.New(slog.DiscardHandler),
	})
	if _, err := projects.Create(ctx, e.admin.User, "libfoo", "fix"); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(base, "wt")
	_ = os.Mkdir(wt, 0o700)
	git := func(args ...string) string {
		cmd := exec.CommandContext(ctx, gitPath, args...)
		cmd.Dir = wt
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + wt, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_AUTHOR_NAME=B", "GIT_AUTHOR_EMAIL=b@example.invalid", "GIT_COMMITTER_NAME=B", "GIT_COMMITTER_EMAIL=b@example.invalid"}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) { _ = os.WriteFile(filepath.Join(wt, name), []byte(content), 0o600) }
	git("init", "-q", "-b", "main")
	write("a.txt", "one\ntwo\nthree\n")
	git("add", ".")
	git("commit", "-q", "-m", "base")
	git("push", "-q", filepath.Join(repos, "libfoo.git"), "main")
	git("checkout", "-q", "-b", "fix")
	write("a.txt", "one\nTWO\nthree\n")
	git("commit", "-q", "-am", "first")
	write("b.txt", "b\n")
	git("add", ".")
	git("commit", "-q", "-m", "second")
	git("push", "-q", filepath.Join(repos, "libfoo.git"), "fix")
	return e, wt
}

func decodeBody[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return v
}

func TestReviewFlow(t *testing.T) {
	e, _ := fullEnv(t)
	c := &client{e: e}
	c.login(t)
	p := basePath + "/projects/libfoo"
	if w := c.do(t, http.MethodGet, basePath+"/projects", nil, nil); !strings.Contains(w.Body.String(), `"clone_url":"`+origin+`/git/libfoo.git"`) {
		t.Fatalf("projects %s", w.Body)
	}
	if w := c.do(t, http.MethodGet, p+"/branches", nil, nil); !strings.Contains(w.Body.String(), "refs/heads/fix") {
		t.Fatalf("branches %s", w.Body)
	}
	w := c.do(t, http.MethodPost, p+"/reviews", map[string]string{"source_branch": "refs/heads/fix", "target_branch": "refs/heads/main", "title": "Fix parsing"}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("open %d %s", w.Code, w.Body)
	}
	detail := c.do(t, http.MethodGet, p+"/reviews/1", nil, nil)
	got := decodeBody[struct {
		Revisions []struct {
			BaseSHA string `json:"base_sha"`
			HeadSHA string `json:"head_sha"`
			Commits []struct {
				SHA string `json:"sha"`
			} `json:"commits"`
		} `json:"revisions"`
	}](t, detail.Body.Bytes())
	if len(got.Revisions) != 1 || len(got.Revisions[0].Commits) != 2 {
		t.Fatalf("detail %s", detail.Body)
	}
	rev := got.Revisions[0]
	diff := c.do(t, http.MethodGet, p+"/reviews/1/diff?rev=1", nil, nil)
	if diff.Code != http.StatusOK || strings.Count(diff.Body.String(), `"new_path"`) != 2 {
		t.Fatalf("diff %d %s", diff.Code, diff.Body)
	}
	if w := c.do(t, http.MethodGet, p+"/reviews/1/diff?commit="+rev.Commits[1].SHA, nil, nil); strings.Count(w.Body.String(), `"new_path"`) != 1 {
		t.Fatalf("commit diff %s", w.Body)
	}
	file := c.do(t, http.MethodGet, p+"/reviews/1/diff/file?from="+rev.BaseSHA+"&to="+rev.HeadSHA+"&old=a.txt&new=a.txt", nil, nil)
	if file.Code != http.StatusOK || !strings.Contains(file.Body.String(), `"text":"TWO"`) {
		t.Fatalf("file diff %d %s", file.Code, file.Body)
	}
	for _, q := range []string{"", "?rev=x", "?commit=nothex", "?from=1"} {
		if w := c.do(t, http.MethodGet, p+"/reviews/1/diff"+q, nil, nil); w.Code != http.StatusBadRequest {
			t.Errorf("diff%s: %d", q, w.Code)
		}
	}
	anchor := map[string]any{"revision": 1, "view": "revision", "from": rev.BaseSHA, "to": rev.HeadSHA, "path": "a.txt", "side": "new", "start_line": 2, "end_line": 2}
	cw := c.do(t, http.MethodPost, p+"/reviews/1/comments", map[string]any{"anchor": anchor, "body": "why caps?"}, nil)
	if cw.Code != http.StatusCreated {
		t.Fatalf("comment %d %s", cw.Code, cw.Body)
	}
	cm := decodeBody[commentJSON](t, cw.Body.Bytes())
	if w := c.do(t, http.MethodPatch, basePath+"/comments/"+cm.ID, map[string]any{"version": 1, "body": "why caps??"}, nil); w.Code != http.StatusOK {
		t.Fatalf("edit %d %s", w.Code, w.Body)
	}
	if w := c.do(t, http.MethodPatch, basePath+"/comments/"+cm.ID, map[string]any{"version": 1, "body": "stale"}, nil); w.Code != http.StatusConflict {
		t.Fatalf("stale edit %d", w.Code)
	}
	if w := c.do(t, http.MethodGet, basePath+"/comments/"+cm.ID+"/versions", nil, nil); strings.Count(w.Body.String(), `"version"`) != 2 {
		t.Fatalf("versions %s", w.Body)
	}
	if w := c.do(t, http.MethodPost, basePath+"/comments/"+cm.ID+"/resolve", map[string]any{"resolved": true}, nil); w.Code != http.StatusNoContent {
		t.Fatalf("resolve %d %s", w.Code, w.Body)
	}
	list := c.do(t, http.MethodGet, p+"/reviews/1/comments?rev=1", nil, nil)
	if !strings.Contains(list.Body.String(), `"resolved":true`) || !strings.Contains(list.Body.String(), `"path":"a.txt"`) {
		t.Fatalf("comments %s", list.Body)
	}
	if w := c.do(t, http.MethodPost, p+"/reviews/1/verdicts", map[string]string{"value": "approve"}, nil); w.Code != http.StatusForbidden {
		t.Fatalf("author approving own review %d", w.Code)
	}
	if w := c.do(t, http.MethodPost, p+"/reviews/1/state", map[string]string{"from": "open", "to": "completed"}, nil); w.Code != http.StatusNoContent {
		t.Fatalf("complete %d %s", w.Code, w.Body)
	}
	if w := c.do(t, http.MethodGet, p+"/reviews?state=completed", nil, nil); strings.Count(w.Body.String(), `"number"`) != 1 {
		t.Fatalf("filtered list %s", w.Body)
	}
	if w := c.do(t, http.MethodGet, p+"/reviews/abc", nil, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("bad review number %d", w.Code)
	}
	if w := c.do(t, http.MethodGet, basePath+"/projects/nope/reviews/1", nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("missing project %d", w.Code)
	}
}

func TestDiscussionFlow(t *testing.T) {
	e, _ := fullEnv(t)
	c := &client{e: e}
	c.login(t)
	ds := basePath + "/projects/libfoo/discussions"
	w := c.do(t, http.MethodPost, ds, map[string]string{"title": "Disclosure plan"}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	d := decodeBody[discussionJSON](t, w.Body.Bytes())
	if w := c.do(t, http.MethodPatch, ds+"/"+d.ID, map[string]string{"title": "Plan v2"}, nil); w.Code != http.StatusNoContent {
		t.Fatalf("rename %d", w.Code)
	}
	if w := c.do(t, http.MethodPost, ds+"/"+d.ID+"/comments", map[string]string{"body": "first"}, nil); w.Code != http.StatusCreated {
		t.Fatalf("comment %d %s", w.Code, w.Body)
	}
	if w := c.do(t, http.MethodGet, ds+"/"+d.ID+"/comments", nil, nil); !strings.Contains(w.Body.String(), `"body":"first"`) {
		t.Fatalf("comments %s", w.Body)
	}
	if w := c.do(t, http.MethodDelete, ds+"/"+d.ID, nil, nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete %d", w.Code)
	}
	if w := c.do(t, http.MethodGet, ds+"/"+d.ID, nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("deleted discussion %d", w.Code)
	}
}
