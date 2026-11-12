package admin

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/garret/internal/attachment"
	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/comment"
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

type nopEvents struct{}

func (nopEvents) Publish(realtime.Event) {}

var (
	root  = auth.User{ID: "01JD00000000000000000ADMIN", Username: "root", Role: auth.RoleAdmin}
	bob   = auth.User{ID: "01JD000000000000000000BOB0", Username: "bob", Role: auth.RoleMember}
	carol = auth.User{ID: "01JD00000000000000000CAROL", Username: "carol", Role: auth.RoleMember}
)

type fixture struct {
	svc         *Service
	db          *store.DB
	comments    *comment.Service
	attachments *attachment.Service
	reposDir    string
	attachDir   string
	projectID   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	base := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(base, "garret.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, u := range []auth.User{root, bob, carol} {
			if _, err := tx.ExecContext(ctx, "INSERT INTO users VALUES (?, ?, 'N', 'secret-hash', ?, 1, NULL)", u.ID, u.Username, string(u.Role)); err != nil {
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
	runner, _ := gitrepo.NewRunner(ctx, gitPath, 2, base)
	repos := filepath.Join(base, "repos")
	attach := filepath.Join(base, "attachments")
	_ = os.Mkdir(repos, 0o700)
	_ = os.Mkdir(attach, 0o700)
	projects := project.New(db, rec, ids, c, runner, repos)
	p, err := projects.Create(ctx, root, "libfoo", "")
	if err != nil {
		t.Fatal(err)
	}
	notifier := inbox.NewNotifier(ids, c)
	reviews := review.NewService(db, rec, ids, c, runner, projects, nopEvents{}, notifier)
	files := attachment.New(attach)
	f := &fixture{
		svc:         New(Config{DB: db, Audit: rec, IDs: ids, Clock: c, Runner: runner, Projects: projects, Files: files}),
		db:          db,
		comments:    comment.New(db, rec, ids, c, runner, projects, nopEvents{}, notifier),
		attachments: attachment.NewService(db, rec, ids, c, files, attachment.DefaultMaxSize),
		reposDir:    repos, attachDir: attach, projectID: p.ID,
	}
	wt := filepath.Join(base, "wt")
	_ = os.Mkdir(wt, 0o700)
	git := func(args ...string) {
		cmd := exec.CommandContext(ctx, gitPath, args...)
		cmd.Dir = wt
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + wt, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_AUTHOR_NAME=B", "GIT_AUTHOR_EMAIL=b@example.invalid", "GIT_COMMITTER_NAME=B", "GIT_COMMITTER_EMAIL=b@example.invalid"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(wt, "a.txt"), []byte("a\n"), 0o600)
	git("add", ".")
	git("commit", "-q", "-m", "base")
	git("push", "-q", filepath.Join(repos, "libfoo.git"), "main")
	git("checkout", "-q", "-b", "fix")
	_ = os.WriteFile(filepath.Join(wt, "a.txt"), []byte("b\n"), 0o600)
	git("commit", "-q", "-am", "fix")
	git("push", "-q", filepath.Join(repos, "libfoo.git"), "fix")
	if _, err := reviews.Open(ctx, bob, "libfoo", "refs/heads/fix", "refs/heads/main", "Fix"); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.ReadTx(context.Background(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(context.Background(), q, args...).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEraseComment(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rootC, _ := f.comments.Create(ctx, carol, "libfoo", comment.NewComment{Review: 1, Body: "secret detail @bob"})
	if _, err := f.comments.Create(ctx, bob, "libfoo", comment.NewComment{Review: 1, ReplyTo: rootC.ID, Body: "reply"}); err != nil {
		t.Fatal(err)
	}
	lone, _ := f.comments.Create(ctx, carol, "libfoo", comment.NewComment{Review: 1, Body: "lone secret"})
	if err := f.svc.EraseComment(ctx, bob, rootC.ID, "leak"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member erased: %v", err)
	}
	if err := f.svc.EraseComment(ctx, root, rootC.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty reason: %v", err)
	}
	if err := f.svc.EraseComment(ctx, root, rootC.ID, "contained exploit details"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.EraseComment(ctx, root, lone.ID, "same"); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, "SELECT count(*) FROM comment_versions WHERE body LIKE '%secret%'"); n != 0 {
		t.Fatalf("%d versions with erased text left", n)
	}
	if n := f.count(t, "SELECT count(*) FROM comments WHERE id = ? AND current_version = 0 AND deleted_at IS NOT NULL", rootC.ID); n != 1 {
		t.Fatal("root with replies not kept as a placeholder")
	}
	if n := f.count(t, "SELECT count(*) FROM comments WHERE id = ?", lone.ID); n != 0 {
		t.Fatal("comment without replies not removed")
	}
	if n := f.count(t, "SELECT count(*) FROM notifications WHERE subject_id = ?", rootC.ID); n != 0 {
		t.Fatal("notifications about the erased comment left")
	}
	if n := f.count(t, "SELECT count(*) FROM erasures WHERE scope = 'comment'"); n != 2 {
		t.Fatalf("%d erasure records", n)
	}
}

func TestEraseAttachment(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a, _ := f.attachments.Upload(ctx, carol, "libfoo", "poc.txt", strings.NewReader("exploit"))
	b, _ := f.attachments.Upload(ctx, carol, "libfoo", "copy.txt", strings.NewReader("exploit"))
	if _, err := f.comments.Create(ctx, carol, "libfoo", comment.NewComment{Review: 1, Body: "see", Attachments: []string{a.ID}}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.attachDir, f.projectID)
	if err := f.svc.EraseAttachment(ctx, root, a.ID, "poc"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatal("file removed while another attachment still uses it")
	}
	if err := f.svc.EraseAttachment(ctx, root, b.ID, "poc"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("file left after its last attachment was erased: %v", entries)
	}
	if n := f.count(t, "SELECT count(*) FROM comment_attachments"); n != 0 {
		t.Fatal("comment links left")
	}
}

func TestExportThenPurge(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.attachments.Upload(ctx, carol, "libfoo", "poc.txt", strings.NewReader("exploit")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.comments.Create(ctx, carol, "libfoo", comment.NewComment{Review: 1, Body: "comment"}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Purge(ctx, root, "libfoo", "libfoo"); !errors.Is(err, ErrExportRequired) {
		t.Fatalf("purge before export: %v", err)
	}
	var archive bytes.Buffer
	sum, err := f.svc.Export(ctx, root, "libfoo", &archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum) != 64 {
		t.Fatalf("digest %q", sum)
	}
	zr, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, zf := range zr.File {
		names[zf.Name] = true
		if zf.Name == "tables/users.json" {
			rc, _ := zf.Open()
			body, _ := io.ReadAll(rc)
			_ = rc.Close()
			if strings.Contains(string(body), "secret-hash") {
				t.Fatal("export contains password hashes")
			}
		}
	}
	for _, want := range []string{"repository.bundle", "tables/reviews.json", "tables/comments.json", "tables/comment_versions.json", "tables/users.json"} {
		if !names[want] {
			t.Errorf("archive lacks %s (has %v)", want, names)
		}
	}
	if err := f.svc.Purge(ctx, root, "libfoo", "libfo"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mistyped name: %v", err)
	}
	if err := f.svc.Purge(ctx, bob, "libfoo", "libfoo"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member purge: %v", err)
	}
	if err := f.svc.Purge(ctx, root, "libfoo", "libfoo"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"projects", "reviews", "revisions", "comments", "comment_versions", "attachments",
		"collab_documents", "notifications", "verdicts", "revision_commits"} {
		if n := f.count(t, "SELECT count(*) FROM "+table); n != 0 {
			t.Errorf("%s has %d rows after purge", table, n)
		}
	}
	if n := f.count(t, "SELECT count(*) FROM audit_events WHERE project_id IS NOT NULL"); n != 0 {
		t.Errorf("%d project audit events left", n)
	}
	if n := f.count(t, "SELECT count(*) FROM erasures WHERE scope = 'project' AND subject_label = 'libfoo'"); n != 1 {
		t.Error("purge not recorded in erasures")
	}
	for _, dir := range []string{filepath.Join(f.reposDir, "libfoo.git"), filepath.Join(f.attachDir, f.projectID)} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s still exists", dir)
		}
	}
}

func TestPurgeRefusesAfterNewActivity(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.svc.Export(ctx, root, "libfoo", io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := f.comments.Create(ctx, carol, "libfoo", comment.NewComment{Review: 1, Body: "late"}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Purge(ctx, root, "libfoo", "libfoo"); !errors.Is(err, ErrExportRequired) {
		t.Fatalf("purge after activity newer than the export: %v", err)
	}
}
