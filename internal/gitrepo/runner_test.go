package gitrepo

import (
	"bytes"
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

func gitPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	return p
}

func newRunner(t *testing.T, limit int) *Runner {
	t.Helper()
	r, err := NewRunner(context.Background(), gitPath(t), limit, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// work runs git in a scratch working tree with a fixed identity.
func work(t *testing.T, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gitPath(t), args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=Ann", "GIT_AUTHOR_EMAIL=ann@example.invalid", "GIT_COMMITTER_NAME=Ann",
		"GIT_COMMITTER_EMAIL=ann@example.invalid", "GIT_AUTHOR_DATE=1700000000 +0000", "GIT_COMMITTER_DATE=1700000000 +0000"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fixture creates a bare repository with three commits on main:
// c1 adds a.txt and old.txt, c2 edits a.txt and renames old.txt, c3 adds a
// file whose name contains a newline.
type fixture struct {
	bare       string
	c1, c2, c3 string
}

func newFixture(t *testing.T, r *Runner) fixture {
	t.Helper()
	base := t.TempDir()
	bare := filepath.Join(base, "p.git")
	if err := r.InitBare(context.Background(), bare); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(base, "wt")
	if err := os.Mkdir(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	work(t, wt, "init", "-q", "-b", "main")
	write(t, filepath.Join(wt, "a.txt"), "one\ntwo\nthree\n")
	write(t, filepath.Join(wt, "old.txt"), "line 1\nline 2\nline 3\nline 4\n")
	work(t, wt, "add", ".")
	work(t, wt, "commit", "-q", "-m", "first")
	c1 := work(t, wt, "rev-parse", "HEAD")
	write(t, filepath.Join(wt, "a.txt"), "one\nTWO\nthree\n")
	work(t, wt, "mv", "old.txt", "new.txt")
	work(t, wt, "commit", "-q", "-am", "second")
	c2 := work(t, wt, "rev-parse", "HEAD")
	write(t, filepath.Join(wt, "odd\nname.txt"), "x\n")
	work(t, wt, "add", ".")
	work(t, wt, "commit", "-q", "-m", "third: subject")
	c3 := work(t, wt, "rev-parse", "HEAD")
	work(t, wt, "push", "-q", bare, "main")
	return fixture{bare: bare, c1: c1, c2: c2, c3: c3}
}

func TestNewRunnerChecksVersion(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "git")
	write(t, old, "#!/bin/sh\necho 'git version 2.37.9'\n")
	if err := os.Chmod(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunner(context.Background(), old, 1, dir); !errors.Is(err, ErrGitVersion) {
		t.Fatalf("err = %v, want ErrGitVersion", err)
	}
	if _, err := NewRunner(context.Background(), filepath.Join(dir, "missing"), 1, dir); err == nil {
		t.Fatal("missing git accepted")
	}
	if _, err := NewRunner(context.Background(), gitPath(t), 0, dir); err == nil {
		t.Fatal("limit 0 accepted")
	}
}

func TestParseGitVersion(t *testing.T) {
	tests := map[string]bool{
		"git version 2.50.1 (Apple Git-155)": true,
		"git version 2.38.0":                 true,
		"git version 2.39.5":                 true,
		"git version 3.0.0":                  true,
		"git version 2.37.9":                 false,
		"git version 1.9.9":                  false,
		"nonsense":                           false,
	}
	for in, want := range tests {
		if got := versionSupported(in); got != want {
			t.Errorf("versionSupported(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestInitBareConfig(t *testing.T) {
	r := newRunner(t, 2)
	f := newFixture(t, r)
	for key, want := range map[string]string{
		"uploadpack.hideRefs":       "refs/garret",
		"receive.hideRefs":          "refs/garret",
		"receive.denyDeleteCurrent": "true",
		"core.bare":                 "true",
	} {
		got := work(t, f.bare, "--git-dir", f.bare, "config", key)
		if got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestRefsAndUpdateRef(t *testing.T) {
	r := newRunner(t, 2)
	f := newFixture(t, r)
	ctx := context.Background()
	refs, err := r.Refs(ctx, f.bare)
	if err != nil {
		t.Fatal(err)
	}
	if refs["refs/heads/main"] != f.c3 || len(refs) != 1 {
		t.Fatalf("refs = %v", refs)
	}
	pin := "refs/garret/reviews/R1/revisions/1"
	if err := r.UpdateRef(ctx, f.bare, pin, f.c2, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateRef(ctx, f.bare, pin, f.c3, f.c1); err == nil {
		t.Fatal("compare-and-swap with a wrong old value succeeded")
	}
	if err := r.UpdateRef(ctx, f.bare, pin, f.c3, f.c2); err != nil {
		t.Fatal(err)
	}
	if refs, _ := r.Refs(ctx, f.bare); len(refs) != 1 {
		t.Fatalf("internal ref listed among branches: %v", refs)
	}
	for _, bad := range []string{"refs/heads/x", "refs/garret/../x", "HEAD", "--force"} {
		if err := r.UpdateRef(ctx, f.bare, bad, f.c3, ""); !errors.Is(err, ErrBadArgument) {
			t.Errorf("UpdateRef(%q) = %v, want ErrBadArgument", bad, err)
		}
	}
}

func TestMergeBaseAndRevList(t *testing.T) {
	r := newRunner(t, 2)
	f := newFixture(t, r)
	ctx := context.Background()
	mb, err := r.MergeBase(ctx, f.bare, f.c1, f.c3)
	if err != nil || mb != f.c1 {
		t.Fatalf("MergeBase = %q, %v; want %s", mb, err, f.c1)
	}
	commits, overflow, err := r.RevList(ctx, f.bare, f.c1, f.c3, 10)
	if err != nil || overflow {
		t.Fatalf("RevList err %v overflow %v", err, overflow)
	}
	var shas, subjects []string
	for _, c := range commits {
		shas = append(shas, c.SHA)
		subjects = append(subjects, c.Subject)
		if c.AuthorName != "Ann" || c.AuthoredAt != 1700000000 {
			t.Errorf("commit %+v", c)
		}
	}
	if !slices.Equal(shas, []string{f.c2, f.c3}) || !slices.Equal(subjects, []string{"second", "third: subject"}) {
		t.Fatalf("commits %v %v", shas, subjects)
	}
	if _, overflow, err := r.RevList(ctx, f.bare, f.c1, f.c3, 1); err != nil || !overflow {
		t.Fatalf("limit 1: overflow %v err %v", overflow, err)
	}
	if _, _, err := r.RevList(ctx, f.bare, "main", f.c3, 1); !errors.Is(err, ErrBadArgument) {
		t.Fatalf("ref name instead of commit: %v", err)
	}
}

func TestDiffSummaryAndFile(t *testing.T) {
	r := newRunner(t, 2)
	f := newFixture(t, r)
	ctx := context.Background()
	files, err := r.DiffSummary(ctx, f.bare, f.c1, f.c3)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]FileDiff{}
	for _, d := range files {
		byPath[d.NewPath] = d
	}
	if d := byPath["a.txt"]; d.Status != StatusModified || d.Added != 1 || d.Deleted != 1 {
		t.Errorf("a.txt %+v", d)
	}
	if d := byPath["new.txt"]; d.Status != StatusRenamed || d.OldPath != "old.txt" {
		t.Errorf("new.txt %+v", d)
	}
	if d := byPath["odd\nname.txt"]; d.Status != StatusAdded || d.Added != 1 {
		t.Errorf("odd name %+v", d)
	}
	hunks, err := r.DiffFile(ctx, f.bare, f.c1, f.c3, "a.txt", "a.txt", Limits{MaxBytes: 1 << 20, MaxLines: 100})
	if err != nil || len(hunks) != 1 || hunks[0].OldLines != 3 {
		t.Fatalf("DiffFile a.txt = %+v, %v", hunks, err)
	}
	hunks, err = r.DiffFile(ctx, f.bare, f.c1, f.c3, "old.txt", "new.txt", Limits{MaxBytes: 1 << 20, MaxLines: 100})
	if err != nil || len(hunks) != 0 {
		t.Fatalf("pure rename should have no hunks: %+v, %v", hunks, err)
	}
	hunks, err = r.DiffFile(ctx, f.bare, f.c1, f.c3, "", "odd\nname.txt", Limits{MaxBytes: 1 << 20, MaxLines: 100})
	if err != nil || len(hunks) != 1 || hunks[0].NewLines != 1 {
		t.Fatalf("added odd file = %+v, %v", hunks, err)
	}
}

func TestFileAt(t *testing.T) {
	r := newRunner(t, 2)
	f := newFixture(t, r)
	ctx := context.Background()
	got, err := r.FileAt(ctx, f.bare, f.c2, "a.txt", 1024)
	if err != nil || string(got) != "one\nTWO\nthree\n" {
		t.Fatalf("FileAt = %q, %v", got, err)
	}
	if _, err := r.FileAt(ctx, f.bare, f.c2, "a.txt", 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over max: %v", err)
	}
	if _, err := r.FileAt(ctx, f.bare, f.c2, "missing.txt", 1024); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing file returned content")
	}
}

func TestBundle(t *testing.T) {
	r := newRunner(t, 2)
	f := newFixture(t, r)
	out := filepath.Join(t.TempDir(), "p.bundle")
	if err := r.Bundle(context.Background(), f.bare, out); err != nil {
		t.Fatal(err)
	}
	if heads := work(t, filepath.Dir(out), "bundle", "list-heads", out); !strings.Contains(heads, f.c3+" refs/heads/main") {
		t.Fatalf("bundle heads %q", heads)
	}
}

// slowGit returns a git wrapper that reports a supported version and sleeps
// on every other command.
func slowGit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "git")
	write(t, p, "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'git version 2.50.0'; exit 0; fi\nexec sleep 5\n")
	if err := os.Chmod(p, 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunnerTimeout(t *testing.T) {
	r, err := NewRunner(context.Background(), slowGit(t), 1, t.TempDir(), WithTimeouts(100*time.Millisecond, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = r.Refs(context.Background(), t.TempDir())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("the timed-out command was not killed")
	}
}

func TestRunnerSemaphoreHonorsContext(t *testing.T) {
	r, err := NewRunner(context.Background(), slowGit(t), 1, t.TempDir(), WithTimeouts(2*time.Second, 2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	busy := make(chan struct{})
	go func() {
		close(busy)
		_, _ = r.Refs(context.Background(), t.TempDir())
	}()
	<-busy
	time.Sleep(100 * time.Millisecond) // let the first command take the only slot
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := r.Refs(ctx, t.TempDir()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline while waiting", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("waiting ignored the context")
	}
}

func TestRunnerOutputCap(t *testing.T) {
	r := newRunner(t, 1)
	f := newFixture(t, r)
	capped, err := NewRunner(context.Background(), gitPath(t), 1, t.TempDir(), WithOutputCap(10))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := capped.Refs(context.Background(), f.bare); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestRunnerErrorCarriesStderrNotArguments(t *testing.T) {
	r := newRunner(t, 1)
	secretPath := filepath.Join(t.TempDir(), "secret-project.git")
	_, err := r.Refs(context.Background(), secretPath)
	if err == nil {
		t.Fatal("expected an error for a missing repository")
	}
	if !strings.Contains(err.Error(), "for-each-ref") {
		t.Errorf("error %q does not name the subcommand", err)
	}
	if strings.Contains(err.Error(), "--git-dir") {
		t.Errorf("error %q repeats the argument list", err)
	}
}

func TestResolveRef(t *testing.T) {
	r := newRunner(t, 2)
	f := newFixture(t, r)
	ctx := context.Background()
	if got, err := r.ResolveRef(ctx, f.bare, "refs/heads/main"); err != nil || got != f.c3 {
		t.Fatalf("main = %q, %v", got, err)
	}
	if got, err := r.ResolveRef(ctx, f.bare, "refs/garret/reviews/R/revisions/1"); err != nil || got != "" {
		t.Fatalf("missing ref = %q, %v", got, err)
	}
	if _, err := r.ResolveRef(ctx, f.bare, "--all"); !errors.Is(err, ErrBadArgument) {
		t.Fatalf("option as ref: %v", err)
	}
}

func TestServeAdvertisesAndRejectsBadInput(t *testing.T) {
	r := newRunner(t, 2)
	f := newFixture(t, r)
	var out bytes.Buffer
	if err := r.Serve(context.Background(), ServiceUploadPack, f.bare, true, "", nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), f.c3+" refs/heads/main") && !strings.Contains(out.String(), f.c3+" HEAD") {
		t.Fatalf("advertisement %q", out.String())
	}
	if err := r.Serve(context.Background(), Service("git-shell"), f.bare, true, "", nil, &out); !errors.Is(err, ErrBadArgument) {
		t.Fatalf("unknown service: %v", err)
	}
	if err := r.Serve(context.Background(), ServiceUploadPack, f.bare, true, "version=2;evil=$(x)", nil, &out); !errors.Is(err, ErrBadArgument) {
		t.Fatalf("bad protocol header: %v", err)
	}
}
