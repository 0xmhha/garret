package project

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
	"github.com/0xmhha/garret/internal/store"
)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) }

var (
	admin  = auth.User{ID: "01JD00000000000000000ADMIN", Username: "root", Role: auth.RoleAdmin}
	member = auth.User{ID: "01JD0000000000000000MEMBER", Username: "bob", Role: auth.RoleMember}
)

func newService(t *testing.T) (*Service, string) {
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
	for _, u := range []auth.User{admin, member} {
		err := db.WriteTx(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(context.Background(),
				"INSERT INTO users VALUES (?, ?, 'N', 'h', ?, 1, NULL)", u.ID, u.Username, string(u.Role))
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	ids, err := id.NewGenerator(fixedClock{}, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := gitrepo.NewRunner(context.Background(), gitPath, 2, base)
	if err != nil {
		t.Fatal(err)
	}
	repos := filepath.Join(base, "repos")
	if err := os.Mkdir(repos, 0o700); err != nil {
		t.Fatal(err)
	}
	return New(db, audit.NewRecorder(ids, fixedClock{}), ids, fixedClock{}, runner, repos), repos
}

func TestCreateAndGet(t *testing.T) {
	s, repos := newService(t)
	ctx := context.Background()
	p, err := s.Create(ctx, admin, "libfoo", "a fix")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "libfoo" || p.ID == "" {
		t.Fatalf("project %+v", p)
	}
	if fi, err := os.Stat(filepath.Join(repos, "libfoo.git", "HEAD")); err != nil || fi.IsDir() {
		t.Fatalf("bare repository missing: %v", err)
	}
	got, err := s.Get(ctx, member, "libfoo")
	if err != nil || got.ID != p.ID {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	list, err := s.List(ctx, member)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	branches, err := s.Branches(ctx, member, "libfoo")
	if err != nil || len(branches) != 0 {
		t.Fatalf("Branches = %v, %v", branches, err)
	}
	if _, err := s.Get(ctx, member, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing project: %v", err)
	}
}

func TestCreateRules(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, member, "libfoo", ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member created a project: %v", err)
	}
	for _, name := range []string{"", "Lib", "../x", "a.git", "a..b"} {
		if _, err := s.Create(ctx, admin, name, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("name %q: %v", name, err)
		}
	}
	if _, err := s.Create(ctx, admin, "libfoo", strings.Repeat("x", 501)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("long description: %v", err)
	}
	if _, err := s.Create(ctx, admin, "libfoo", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, admin, "libfoo", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestDuplicateCreateKeepsTheFirstRepository(t *testing.T) {
	s, repos := newService(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			_, err := s.Create(ctx, admin, "libfoo", "")
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	ok := 0
	for err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatalf("unexpected %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d creates succeeded", ok)
	}
	if _, err := os.Stat(filepath.Join(repos, "libfoo.git", "HEAD")); err != nil {
		t.Fatalf("the surviving project's repository was removed: %v", err)
	}
}

func TestRemoveOrphanRepositories(t *testing.T) {
	s, repos := newService(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, admin, "kept", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repos, "orphan.git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repos, "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := s.RemoveOrphanRepositories(ctx)
	if err != nil || len(removed) != 1 || removed[0] != "orphan.git" {
		t.Fatalf("removed %v, %v", removed, err)
	}
	for _, keep := range []string{"kept.git", "notes.txt"} {
		if _, err := os.Stat(filepath.Join(repos, keep)); err != nil {
			t.Fatalf("%s removed: %v", keep, err)
		}
	}
}
