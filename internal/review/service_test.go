package review

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/gitrepo"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/inbox"
	"github.com/0xmhha/garret/internal/project"
	"github.com/0xmhha/garret/internal/realtime"
	"github.com/0xmhha/garret/internal/store"
)

type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Millisecond)
	return c.now
}

type events struct {
	mu   sync.Mutex
	list []realtime.Event
}

func (e *events) Publish(ev realtime.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, ev)
}

func (e *events) types() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, ev := range e.list {
		out = append(out, ev.Type)
	}
	return out
}

var (
	admin = auth.User{ID: "01JD00000000000000000ADMIN", Username: "root", Role: auth.RoleAdmin}
	bob   = auth.User{ID: "01JD000000000000000000BOB0", Username: "bob", Role: auth.RoleMember}
	carol = auth.User{ID: "01JD00000000000000000CAROL", Username: "carol", Role: auth.RoleMember}
)

type fixture struct {
	svc    *Service
	db     *store.DB
	runner *gitrepo.Runner
	events *events
	repo   string // bare repository
	wt     string // working tree pushing to repo
	git    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(base, "garret.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, u := range []auth.User{admin, bob, carol} {
		if err := db.WriteTx(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(context.Background(), "INSERT INTO users VALUES (?, ?, 'N', 'h', ?, 1, NULL)", u.ID, u.Username, string(u.Role))
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	c := &stepClock{now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
	ids, _ := id.NewGenerator(c, rand.Reader)
	rec := audit.NewRecorder(ids, c)
	runner, err := gitrepo.NewRunner(context.Background(), gitPath, 2, base)
	if err != nil {
		t.Fatal(err)
	}
	repos := filepath.Join(base, "repos")
	if err := os.Mkdir(repos, 0o700); err != nil {
		t.Fatal(err)
	}
	projects := project.New(db, rec, ids, c, runner, repos)
	if _, err := projects.Create(context.Background(), admin, "libfoo", ""); err != nil {
		t.Fatal(err)
	}
	ev := &events{}
	f := &fixture{
		svc: NewService(db, rec, ids, c, runner, projects, ev, inbox.NewNotifier(ids, c)), db: db, runner: runner, events: ev,
		repo: filepath.Join(repos, "libfoo.git"), wt: filepath.Join(base, "wt"), git: gitPath,
	}
	if err := os.Mkdir(f.wt, 0o700); err != nil {
		t.Fatal(err)
	}
	f.run(t, "init", "-q", "-b", "main")
	f.commit(t, "a.txt", "one\ntwo\nthree\n", "base")
	f.run(t, "push", "-q", f.repo, "main")
	return f
}

func (f *fixture) run(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.git, args...)
	cmd.Dir = f.wt
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + f.wt, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=Bob", "GIT_AUTHOR_EMAIL=bob@example.invalid", "GIT_COMMITTER_NAME=Bob", "GIT_COMMITTER_EMAIL=bob@example.invalid"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) commit(t *testing.T, file, content, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.wt, file), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	f.run(t, "add", file)
	f.run(t, "commit", "-q", "-m", msg)
	return f.run(t, "rev-parse", "HEAD")
}

// branch pushes three commits on fix and returns the head.
func (f *fixture) branch(t *testing.T) string {
	t.Helper()
	f.run(t, "checkout", "-q", "-b", "fix")
	f.commit(t, "a.txt", "one\nTWO\nthree\n", "first")
	f.commit(t, "b.txt", "b\n", "second")
	head := f.commit(t, "c.txt", "c\n", "third")
	f.run(t, "push", "-q", f.repo, "fix")
	return head
}

func (f *fixture) open(t *testing.T) Review {
	t.Helper()
	f.branch(t)
	r, err := f.svc.Open(context.Background(), bob, "libfoo", "refs/heads/fix", "refs/heads/main", "Fix parsing")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOpen(t *testing.T) {
	f := newFixture(t)
	head := f.branch(t)
	r, err := f.svc.Open(context.Background(), bob, "libfoo", "refs/heads/fix", "refs/heads/main", "Fix parsing")
	if err != nil {
		t.Fatal(err)
	}
	if r.Number != 1 || r.State != StateOpen || r.AuthorID != bob.ID {
		t.Fatalf("review %+v", r)
	}
	d, err := f.svc.Get(context.Background(), carol, "libfoo", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Revisions) != 1 || d.Revisions[0].HeadSHA != head || len(d.Revisions[0].Commits) != 3 {
		t.Fatalf("revisions %+v", d.Revisions)
	}
	if subj := d.Revisions[0].Commits[0].Subject; subj != "first" {
		t.Fatalf("oldest commit first, got %q", subj)
	}
	refs := f.run(t, "--git-dir", f.repo, "for-each-ref", "--format=%(refname) %(objectname)", "refs/garret")
	if !strings.Contains(refs, "/revisions/1 "+head) {
		t.Fatalf("revision not pinned: %q", refs)
	}
	if got := f.events.types(); len(got) != 1 || got[0] != "review.created" {
		t.Fatalf("events %v", got)
	}
	r2, err := f.svc.Open(context.Background(), bob, "libfoo", "refs/heads/fix", "refs/heads/main", "Again")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("second open review on one branch: %+v, %v", r2, err)
	}
}

func TestOpenValidation(t *testing.T) {
	f := newFixture(t)
	f.branch(t)
	ctx := context.Background()
	cases := []struct {
		name, src, dst, title string
		want                  error
	}{
		{"empty title", "refs/heads/fix", "refs/heads/main", "", ErrInvalid},
		{"long title", "refs/heads/fix", "refs/heads/main", strings.Repeat("x", 201), ErrInvalid},
		{"same branch", "refs/heads/main", "refs/heads/main", "t", ErrInvalid},
		{"bad ref", "fix", "refs/heads/main", "t", ErrInvalid},
		{"missing branch", "refs/heads/nope", "refs/heads/main", "t", ErrNotFound},
	}
	for _, c := range cases {
		if _, err := f.svc.Open(ctx, bob, "libfoo", c.src, c.dst, c.title); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	if _, err := f.svc.Open(ctx, bob, "nope", "refs/heads/fix", "refs/heads/main", "t"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing project: %v", err)
	}
	f.run(t, "checkout", "-q", "-b", "empty", "main")
	f.run(t, "push", "-q", f.repo, "empty")
	if _, err := f.svc.Open(ctx, bob, "libfoo", "refs/heads/empty", "refs/heads/main", "t"); !errors.Is(err, ErrInvalid) {
		t.Errorf("branch without commits: %v", err)
	}
}

func TestHandlePushCreatesRevision(t *testing.T) {
	f := newFixture(t)
	r := f.open(t)
	ctx := context.Background()
	f.run(t, "commit", "-q", "--amend", "-m", "third, amended")
	head := f.run(t, "rev-parse", "HEAD")
	old := f.run(t, "--git-dir", f.repo, "rev-parse", "refs/heads/fix")
	f.run(t, "push", "-q", "-f", f.repo, "fix")
	changes := []RefChange{{Ref: "refs/heads/fix", Old: old, New: head}, {Ref: "refs/heads/other", Old: old, New: head}}
	if err := f.svc.HandlePush(ctx, "libfoo", bob, changes); err != nil {
		t.Fatal(err)
	}
	d, _ := f.svc.Get(ctx, bob, "libfoo", r.Number)
	if len(d.Revisions) != 2 || d.Revisions[1].HeadSHA != head || d.Revisions[1].Number != 2 {
		t.Fatalf("revisions %+v", d.Revisions)
	}
	// The force-pushed old head stays reachable through its pin.
	f.run(t, "--git-dir", f.repo, "cat-file", "-e", old+"^{commit}")
	if err := f.svc.HandlePush(ctx, "libfoo", bob, changes[:1]); err != nil {
		t.Fatal(err)
	}
	if d, _ := f.svc.Get(ctx, bob, "libfoo", r.Number); len(d.Revisions) != 2 {
		t.Fatal("a push without a head change created a revision")
	}
	if err := f.svc.HandlePush(ctx, "libfoo", bob, []RefChange{{Ref: "refs/heads/fix", Old: head, New: strings.Repeat("0", 40)}}); err != nil {
		t.Fatalf("branch deletion: %v", err)
	}
}

func TestHandlePushConcurrentKeepsNumbersUnique(t *testing.T) {
	f := newFixture(t)
	r := f.open(t)
	head := f.commit(t, "d.txt", "d\n", "fourth")
	f.run(t, "push", "-q", f.repo, "fix")
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			_ = f.svc.HandlePush(context.Background(), "libfoo", bob, []RefChange{{Ref: "refs/heads/fix", New: head}})
		})
	}
	wg.Wait()
	d, _ := f.svc.Get(context.Background(), bob, "libfoo", r.Number)
	if len(d.Revisions) != 2 {
		t.Fatalf("%d revisions after five identical pushes", len(d.Revisions))
	}
}

func TestVerdicts(t *testing.T) {
	f := newFixture(t)
	r := f.open(t)
	ctx := context.Background()
	if err := f.svc.RecordVerdict(ctx, bob, "libfoo", r.Number, VerdictApprove); !errors.Is(err, ErrForbidden) {
		t.Fatalf("author approved own review: %v", err)
	}
	if err := f.svc.RecordVerdict(ctx, bob, "libfoo", r.Number, VerdictRequestChanges); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RecordVerdict(ctx, carol, "libfoo", r.Number, VerdictRequestChanges); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RecordVerdict(ctx, carol, "libfoo", r.Number, VerdictApprove); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RecordVerdict(ctx, carol, "libfoo", r.Number, Verdict("maybe")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown verdict: %v", err)
	}
	d, _ := f.svc.Get(ctx, bob, "libfoo", r.Number)
	got := map[string]Verdict{}
	for _, v := range d.Verdicts {
		got[v.UserID] = v.Value
	}
	if got[carol.ID] != VerdictApprove || got[bob.ID] != VerdictRequestChanges || len(got) != 2 {
		t.Fatalf("latest verdicts %v", got)
	}
}

func TestChangeState(t *testing.T) {
	f := newFixture(t)
	r := f.open(t)
	ctx := context.Background()
	if err := f.svc.ChangeState(ctx, carol, "libfoo", r.Number, StateOpen, StateCompleted); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ChangeState(ctx, carol, "libfoo", r.Number, StateOpen, StateAbandoned); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale from-state: %v", err)
	}
	if err := f.svc.RecordVerdict(ctx, carol, "libfoo", r.Number, VerdictApprove); !errors.Is(err, ErrConflict) {
		t.Fatalf("verdict on a completed review: %v", err)
	}
	if err := f.svc.ChangeState(ctx, bob, "libfoo", r.Number, StateCompleted, StateAbandoned); !errors.Is(err, ErrInvalid) {
		t.Fatalf("completed to abandoned: %v", err)
	}
	if err := f.svc.ChangeState(ctx, bob, "libfoo", r.Number, StateCompleted, StateOpen); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	var n int
	_ = f.db.ReadTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT count(*) FROM review_state_changes").Scan(&n)
	})
	if n != 2 {
		t.Fatalf("%d state changes recorded, want 2", n)
	}
}

func TestRepairPins(t *testing.T) {
	f := newFixture(t)
	r := f.open(t)
	ctx := context.Background()
	f.run(t, "--git-dir", f.repo, "update-ref", "-d", "refs/garret/reviews/"+r.ID+"/revisions/1")
	n, err := f.svc.RepairPins(ctx)
	if err != nil || n != 1 {
		t.Fatalf("repaired %d, %v", n, err)
	}
	if n, _ := f.svc.RepairPins(ctx); n != 0 {
		t.Fatalf("second repair changed %d pins", n)
	}
}
