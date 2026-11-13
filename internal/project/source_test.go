package project

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// git runs git in dir with a fixed identity, as a member's terminal would.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=Ann", "GIT_AUTHOR_EMAIL=ann@example.invalid", "GIT_COMMITTER_NAME=Ann", "GIT_COMMITTER_EMAIL=ann@example.invalid"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// localRepo is a working repository with main and topic.
func localRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "fix")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	git(t, dir, "checkout", "-q", "-b", "topic")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "fix")
	git(t, dir, "checkout", "-q", "main")
	// The base branch only as a remote-tracking branch, as in many clones.
	git(t, dir, "update-ref", "refs/remotes/origin/master", git(t, dir, "rev-parse", "main"))
	return dir
}

func heads(t *testing.T, s *Service, name string) map[string]string {
	t.Helper()
	dir, _ := s.RepoPath(name)
	refs, err := s.runner.Refs(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return refs
}

func TestPreview(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	src := localRepo(t)
	got, err := s.Preview(ctx, admin, src)
	if err != nil || !slices.Equal(got, []string{"main", "origin/master", "topic"}) {
		t.Fatalf("preview %v, %v", got, err)
	}
	if _, err := s.Preview(ctx, member, src); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member preview: %v", err)
	}
	if _, err := s.Preview(ctx, admin, t.TempDir()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("not a repository: %v", err)
	}
	if _, err := s.Preview(ctx, admin, "relative/path"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("relative path: %v", err)
	}
}

func TestCreateFromSource(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	src := localRepo(t)
	p, err := s.CreateFromSource(ctx, admin, "libfoo", "fix", src)
	if err != nil {
		t.Fatal(err)
	}
	if p.SourcePath != src {
		t.Fatalf("source %q", p.SourcePath)
	}
	got := heads(t, s, "libfoo")
	if got["refs/heads/main"] != git(t, src, "rev-parse", "main") || got["refs/heads/topic"] != git(t, src, "rev-parse", "topic") ||
		got["refs/heads/origin/master"] != git(t, src, "rev-parse", "refs/remotes/origin/master") {
		t.Fatalf("heads %v", got)
	}
	if again, _ := s.Get(ctx, member, "libfoo"); again.SourcePath != src {
		t.Fatalf("stored source %q", again.SourcePath)
	}
	if _, err := s.CreateFromSource(ctx, member, "other", "", src); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member: %v", err)
	}
	// A bad source leaves no project and no repository behind.
	if _, err := s.CreateFromSource(ctx, admin, "broken", "", t.TempDir()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad source: %v", err)
	}
	if _, err := s.Get(ctx, admin, "broken"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("project left behind: %v", err)
	}
	if dir, _ := s.RepoPath("broken"); dirExists(dir) {
		t.Fatal("repository left behind")
	}
}

func TestSync(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	src := localRepo(t)
	if _, err := s.CreateFromSource(ctx, admin, "libfoo", "", src); err != nil {
		t.Fatal(err)
	}
	oldTopic := git(t, src, "rev-parse", "topic")
	git(t, src, "commit", "-q", "--allow-empty", "-m", "more on main")
	git(t, src, "checkout", "-q", "-b", "extra")
	git(t, src, "commit", "-q", "--allow-empty", "-m", "extra")
	git(t, src, "checkout", "-q", "topic")
	git(t, src, "commit", "-q", "--amend", "--allow-empty", "-m", "fix, rewritten")
	git(t, src, "branch", "-q", "-D", "extra")
	git(t, src, "checkout", "-q", "-b", "extra2")

	res, err := s.Sync(ctx, member, "libfoo", false)
	if err != nil {
		t.Fatal(err)
	}
	want := SyncResult{Created: []string{"extra2"}, Updated: []string{"main"}, Rewritten: []string{"topic"}}
	if !slices.Equal(res.Created, want.Created) || !slices.Equal(res.Updated, want.Updated) || !slices.Equal(res.Rewritten, want.Rewritten) {
		t.Fatalf("result %+v", res)
	}
	if len(res.Moves) != 2 {
		t.Fatalf("moves %+v", res.Moves)
	}
	got := heads(t, s, "libfoo")
	if got["refs/heads/topic"] != oldTopic {
		t.Fatal("a rewritten branch moved without overwrite")
	}
	res, err = s.Sync(ctx, member, "libfoo", true)
	if err != nil || !slices.Equal(res.Updated, []string{"topic"}) || len(res.Rewritten) != 0 {
		t.Fatalf("overwrite %+v, %v", res, err)
	}
	if heads(t, s, "libfoo")["refs/heads/topic"] != git(t, src, "rev-parse", "topic") {
		t.Fatal("overwrite did not move topic")
	}
	all, _ := s.runner.AllRefs(ctx, mustRepo(t, s, "libfoo"))
	for ref := range all {
		if strings.HasPrefix(ref, "refs/garret/import/") {
			t.Fatalf("staging ref left: %s", ref)
		}
	}
	res, err = s.Sync(ctx, member, "libfoo", false)
	if err != nil || len(res.Moves) != 0 || len(res.Unchanged) != 4 {
		t.Fatalf("second sync %+v, %v", res, err)
	}
}

func TestSyncWithoutSource(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, admin, "plain", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(ctx, admin, "plain", false); !errors.Is(err, ErrNoSource) {
		t.Fatalf("err = %v", err)
	}
	src := localRepo(t)
	if _, err := s.CreateFromSource(ctx, admin, "gone", "", src); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(ctx, admin, "gone", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("source removed: %v", err)
	}
}

func mustRepo(t *testing.T, s *Service, name string) string {
	t.Helper()
	dir, err := s.RepoPath(name)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
