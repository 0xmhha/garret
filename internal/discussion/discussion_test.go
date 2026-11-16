package discussion

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/id"
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

type nopEvents struct{}

func (nopEvents) Publish(realtime.Event) {}

var (
	admin = auth.User{ID: "01JD00000000000000000ADMIN", Username: "root", Role: auth.RoleAdmin}
	bob   = auth.User{ID: "01JD000000000000000000BOB0", Username: "bob", Role: auth.RoleMember}
	carol = auth.User{ID: "01JD00000000000000000CAROL", Username: "carol", Role: auth.RoleMember}
)

func setup(t *testing.T) *Service {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "garret.db"))
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
		_, err := tx.ExecContext(ctx, "INSERT INTO projects VALUES ('01JD0000000000000000PROJ01', 'libfoo', '', ?, 1, 1, NULL)", admin.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	c := &stepClock{now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
	ids, _ := id.NewGenerator(c, rand.Reader)
	return New(db, audit.NewRecorder(ids, c), ids, c, nopEvents{})
}

func TestCreateRenameDelete(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	d, err := s.Create(ctx, bob, "libfoo", "Disclosure plan")
	if err != nil {
		t.Fatal(err)
	}
	if d.BodyDocID == "" || d.AuthorID != bob.ID {
		t.Fatalf("discussion %+v", d)
	}
	if err := s.Rename(ctx, carol, "libfoo", d.ID, "Hijacked"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-author rename: %v", err)
	}
	if err := s.Rename(ctx, bob, "libfoo", d.ID, "Disclosure plan v2"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, carol, "libfoo", d.ID)
	if err != nil || got.Title != "Disclosure plan v2" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if err := s.Delete(ctx, carol, "libfoo", d.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-author delete: %v", err)
	}
	if err := s.Delete(ctx, admin, "libfoo", d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, carol, "libfoo", d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted discussion visible: %v", err)
	}
	if list, _ := s.List(ctx, bob, "libfoo"); len(list) != 0 {
		t.Fatalf("deleted discussion listed: %+v", list)
	}
}

func TestValidation(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	for _, title := range []string{"", "  ", strings.Repeat("x", 201)} {
		if _, err := s.Create(ctx, bob, "libfoo", title); !errors.Is(err, ErrInvalid) {
			t.Errorf("title %q: %v", title, err)
		}
	}
	if _, err := s.Create(ctx, bob, "missing", "t"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing project: %v", err)
	}
}
