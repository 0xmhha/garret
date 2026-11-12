package comment

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
	"github.com/0xmhha/garret/internal/review"
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

var (
	admin = auth.User{ID: "01JD00000000000000000ADMIN", Username: "root", Role: auth.RoleAdmin}
	bob   = auth.User{ID: "01JD000000000000000000BOB0", Username: "bob", Role: auth.RoleMember}
	carol = auth.User{ID: "01JD00000000000000000CAROL", Username: "carol", Role: auth.RoleMember}
)

type fixture struct {
	db      *store.DB
	svc     *Service
	inbox   *inbox.Service
	events  *events
	rev     review.Detail
	wt, git string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(base, "garret.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, u := range []auth.User{admin, bob, carol} {
			if _, err := tx.ExecContext(ctx, "INSERT INTO users VALUES (?, ?, 'N', 'h', ?, 1, NULL)", u.ID, u.Username, string(u.Role)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c := &stepClock{now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
	ids, _ := id.NewGenerator(c, rand.Reader)
	rec := audit.NewRecorder(ids, c)
	runner, err := gitrepo.NewRunner(ctx, gitPath, 2, base)
	if err != nil {
		t.Fatal(err)
	}
	repos := filepath.Join(base, "repos")
	_ = os.Mkdir(repos, 0o700)
	projects := project.New(db, rec, ids, c, runner, repos)
	if _, err := projects.Create(ctx, admin, "libfoo", ""); err != nil {
		t.Fatal(err)
	}
	ev := &events{}
	in := inbox.NewService(db, inbox.NewNotifier(ids, c), c)
	reviews := review.NewService(db, rec, ids, c, runner, projects, ev, in.Notifier())
	f := &fixture{db: db, svc: New(db, rec, ids, c, runner, projects, ev, in.Notifier()), inbox: in, events: ev,
		wt: filepath.Join(base, "wt"), git: gitPath}
	_ = os.Mkdir(f.wt, 0o700)
	f.run(t, "init", "-q", "-b", "main")
	f.write(t, "a.txt", "one\ntwo\nthree\n")
	f.run(t, "add", ".")
	f.run(t, "commit", "-q", "-m", "base")
	f.run(t, "push", "-q", filepath.Join(repos, "libfoo.git"), "main")
	f.run(t, "checkout", "-q", "-b", "fix")
	f.write(t, "a.txt", "one\nTWO\nthree\nfour\n")
	f.run(t, "commit", "-q", "-am", "edit")
	f.run(t, "push", "-q", filepath.Join(repos, "libfoo.git"), "fix")
	if _, err := reviews.Open(ctx, bob, "libfoo", "refs/heads/fix", "refs/heads/main", "Fix"); err != nil {
		t.Fatal(err)
	}
	if f.rev, err = reviews.Get(ctx, bob, "libfoo", 1); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.wt, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) run(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.git, args...)
	cmd.Dir = f.wt
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + f.wt, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=B", "GIT_AUTHOR_EMAIL=b@example.invalid", "GIT_COMMITTER_NAME=B", "GIT_COMMITTER_EMAIL=b@example.invalid"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func (f *fixture) anchor(start, end int) *Anchor {
	r := f.rev.Revisions[0]
	return &Anchor{Revision: 1, View: ViewRevision, From: r.BaseSHA, To: r.HeadSHA, Path: "a.txt", Side: SideNew, Start: start, End: end}
}

func (f *fixture) unread(t *testing.T, u auth.User) []inbox.Notification {
	t.Helper()
	list, err := f.inbox.List(context.Background(), u, true, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestCreateLineCommentAndNotify(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	c, err := f.svc.Create(ctx, carol, "libfoo", NewComment{Review: 1, Anchor: f.anchor(2, 3), Body: "why TWO? cc @root and @nobody and @carol"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Anchor == nil || c.Anchor.Start != 2 || c.Version != 1 {
		t.Fatalf("comment %+v", c)
	}
	kinds := func(u auth.User) []inbox.Kind {
		var out []inbox.Kind
		for _, n := range f.unread(t, u) {
			out = append(out, n.Kind)
		}
		return out
	}
	if got := kinds(bob); len(got) != 1 || got[0] != inbox.KindCommentOnMyReview {
		t.Fatalf("review author notifications %v", got)
	}
	if got := kinds(admin); len(got) != 1 || got[0] != inbox.KindMention {
		t.Fatalf("mentioned user notifications %v", got)
	}
	if got := kinds(carol); len(got) != 0 {
		t.Fatalf("author notified of own comment: %v", got)
	}
}

func TestReplies(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	root, _ := f.svc.Create(ctx, carol, "libfoo", NewComment{Review: 1, Body: "root"})
	if _, err := f.svc.Create(ctx, bob, "libfoo", NewComment{Review: 1, ReplyTo: root.ID, Body: "reply"}); err != nil {
		t.Fatal(err)
	}
	if got := f.unread(t, carol); len(got) != 1 || got[0].Kind != inbox.KindReplyToMe {
		t.Fatalf("root author notifications %+v", got)
	}
	reply, _ := f.svc.Create(ctx, carol, "libfoo", NewComment{Review: 1, ReplyTo: root.ID, Body: "again"})
	if _, err := f.svc.Create(ctx, bob, "libfoo", NewComment{Review: 1, ReplyTo: reply.ID, Body: "nested"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reply to a reply: %v", err)
	}
	if _, err := f.svc.Create(ctx, bob, "libfoo", NewComment{Review: 1, ReplyTo: root.ID, Anchor: f.anchor(1, 1), Body: "x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("anchored reply: %v", err)
	}
	list, err := f.svc.ListForReview(ctx, bob, "libfoo", 1)
	if err != nil || len(list) != 3 || list[1].ThreadRootID != root.ID {
		t.Fatalf("list %+v, %v", list, err)
	}
}

func TestAnchorValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := f.rev.Revisions[0]
	cases := map[string]*Anchor{
		"past the end":       f.anchor(4, 5),
		"start zero end set": {Revision: 1, View: ViewRevision, From: r.BaseSHA, To: r.HeadSHA, Path: "a.txt", Side: SideNew, Start: 0, End: 2},
		"end before start":   f.anchor(3, 2),
		"missing file":       {Revision: 1, View: ViewRevision, From: r.BaseSHA, To: r.HeadSHA, Path: "zzz.txt", Side: SideNew, Start: 1, End: 1},
		"wrong commits":      {Revision: 1, View: ViewRevision, From: r.HeadSHA, To: r.BaseSHA, Path: "a.txt", Side: SideNew, Start: 1, End: 1},
		"unknown revision":   {Revision: 9, View: ViewRevision, From: r.BaseSHA, To: r.HeadSHA, Path: "a.txt", Side: SideNew, Start: 1, End: 1},
		"bad side":           {Revision: 1, View: ViewRevision, From: r.BaseSHA, To: r.HeadSHA, Path: "a.txt", Side: Side("both"), Start: 1, End: 1},
		"old side past end":  {Revision: 1, View: ViewRevision, From: r.BaseSHA, To: r.HeadSHA, Path: "a.txt", Side: SideOld, Start: 4, End: 4},
	}
	for name, a := range cases {
		if _, err := f.svc.Create(ctx, carol, "libfoo", NewComment{Review: 1, Anchor: a, Body: "x"}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	file := f.anchor(0, 0)
	if _, err := f.svc.Create(ctx, carol, "libfoo", NewComment{Review: 1, Anchor: file, Body: "file level"}); err != nil {
		t.Fatalf("file-level anchor: %v", err)
	}
	commit := &Anchor{Revision: 1, View: ViewCommit, From: r.BaseSHA, To: r.Commits[0].SHA, Path: "a.txt", Side: SideNew, Start: 4, End: 4}
	if _, err := f.svc.Create(ctx, carol, "libfoo", NewComment{Review: 1, Anchor: commit, Body: "commit view"}); err != nil {
		t.Fatalf("commit anchor: %v", err)
	}
}

func TestBodyLimits(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for name, body := range map[string]string{"empty": "", "too long": strings.Repeat("x", 65537), "invalid UTF-8": "\xff"} {
		if _, err := f.svc.Create(ctx, carol, "libfoo", NewComment{Review: 1, Body: body}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := f.svc.Create(ctx, carol, "libfoo", NewComment{Review: 1, Body: strings.Repeat("x", 65536)}); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	if _, err := f.svc.Create(ctx, carol, "libfoo", NewComment{Review: 7, Body: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing review: %v", err)
	}
}

func TestEditVersionsAndMentions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	c, _ := f.svc.Create(ctx, carol, "libfoo", NewComment{Review: 1, Body: "first"})
	if _, err := f.svc.Edit(ctx, bob, c.ID, 1, "hijack"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-author edit: %v", err)
	}
	edited, err := f.svc.Edit(ctx, carol, c.ID, 1, "second, cc @root")
	if err != nil || edited.Version != 2 || edited.Body != "second, cc @root" {
		t.Fatalf("Edit = %+v, %v", edited, err)
	}
	if _, err := f.svc.Edit(ctx, carol, c.ID, 1, "stale"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale edit: %v", err)
	}
	if _, err := f.svc.Edit(ctx, carol, c.ID, 2, "third, cc @root"); err != nil {
		t.Fatal(err)
	}
	if got := f.unread(t, admin); len(got) != 1 {
		t.Fatalf("mention notified %d times, want once", len(got))
	}
	versions, err := f.svc.Versions(ctx, bob, c.ID)
	if err != nil || len(versions) != 3 || versions[0].Body != "first" {
		t.Fatalf("versions %+v, %v", versions, err)
	}
}

func TestResolveAndDelete(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	root, _ := f.svc.Create(ctx, carol, "libfoo", NewComment{Review: 1, Body: "root"})
	reply, _ := f.svc.Create(ctx, bob, "libfoo", NewComment{Review: 1, ReplyTo: root.ID, Body: "reply"})
	if err := f.svc.Resolve(ctx, bob, root.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Resolve(ctx, bob, reply.ID, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("resolve a reply: %v", err)
	}
	if err := f.svc.Delete(ctx, bob, root.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-author delete: %v", err)
	}
	if err := f.svc.Delete(ctx, admin, root.ID); err != nil {
		t.Fatalf("admin delete: %v", err)
	}
	list, _ := f.svc.ListForReview(ctx, bob, "libfoo", 1)
	if !list[0].Deleted || list[0].Body != "" || list[0].Resolved != true {
		t.Fatalf("deleted root %+v", list[0])
	}
	if _, err := f.svc.Edit(ctx, carol, root.ID, 1, "x"); !errors.Is(err, ErrConflict) {
		t.Fatalf("edit after delete: %v", err)
	}
	if _, err := f.svc.Create(ctx, carol, "libfoo", NewComment{Review: 1, ReplyTo: root.ID, Body: "x"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("reply to a deleted root: %v", err)
	}
}

func TestCommentAttachments(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	insert := func(id, projectID string, deleted bool) {
		var del any
		if deleted {
			del = 5
		}
		err := f.db.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO attachments VALUES (?, ?, ?, 'poc.txt', 'text/plain', 1, zeroblob(32), 1, ?)`,
				id, projectID, carol.ID, del)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	p := f.rev.ProjectID
	insert("01JD0000000000000000ATTAC1", p, false)
	insert("01JD0000000000000000ATTAC2", p, true)
	c, err := f.svc.Create(ctx, carol, "libfoo", NewComment{Review: 1, Body: "see file", Attachments: []string{"01JD0000000000000000ATTAC1"}})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := f.svc.ListForReview(ctx, bob, "libfoo", 1)
	if len(list) != 1 || len(list[0].Attachments) != 1 || list[0].Attachments[0] != "01JD0000000000000000ATTAC1" || c.ID != list[0].ID {
		t.Fatalf("listed %+v", list)
	}
	for _, bad := range []string{"01JD0000000000000000ATTAC2", "01JD0000000000000000NOPE00"} {
		if _, err := f.svc.Create(ctx, carol, "libfoo", NewComment{Review: 1, Body: "x", Attachments: []string{bad}}); !errors.Is(err, ErrInvalid) {
			t.Errorf("attachment %s: %v", bad, err)
		}
	}
}
