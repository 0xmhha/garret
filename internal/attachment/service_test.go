package attachment

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/id"
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

var (
	admin = auth.User{ID: "01JD00000000000000000ADMIN", Username: "root", Role: auth.RoleAdmin}
	bob   = auth.User{ID: "01JD000000000000000000BOB0", Username: "bob", Role: auth.RoleMember}
	carol = auth.User{ID: "01JD00000000000000000CAROL", Username: "carol", Role: auth.RoleMember}
)

func newService(t *testing.T) *Service {
	t.Helper()
	ctx := context.Background()
	base := t.TempDir()
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
		_, err := tx.ExecContext(ctx, "INSERT INTO projects VALUES (?, 'libfoo', '', ?, 1, 1)", project, admin.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s, _ := newStore(t)
	c := &stepClock{now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
	ids, _ := id.NewGenerator(c, rand.Reader)
	return NewService(db, audit.NewRecorder(ids, c), ids, c, s, DefaultMaxSize)
}

func TestUploadAndDownload(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	a, err := s.Upload(ctx, bob, "libfoo", "../../etc/poc\x00.png", strings.NewReader(string(png)))
	if err != nil {
		t.Fatal(err)
	}
	if a.Filename != "....etcpoc.png" || a.MediaType != "image/png" || !a.Inline {
		t.Fatalf("attachment %+v", a)
	}
	got, f, err := s.Open(ctx, carol, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	content, _ := io.ReadAll(f)
	if got.ID != a.ID || string(content) != string(png) {
		t.Fatalf("download %+v %q", got, content)
	}
	html, err := s.Upload(ctx, bob, "libfoo", "x.html", strings.NewReader("<html><script>alert(1)</script>"))
	if err != nil || html.Inline {
		t.Fatalf("HTML upload %+v, %v (must never be inline)", html, err)
	}
}

func TestUploadRules(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	if _, err := s.Upload(ctx, bob, "missing", "a.txt", strings.NewReader("x")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing project: %v", err)
	}
	s.maxSize = 4
	if _, err := s.Upload(ctx, bob, "libfoo", "a.txt", strings.NewReader("12345")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the limit: %v", err)
	}
	for _, name := range []string{"", "/", "\x01\x02"} {
		a, err := s.Upload(ctx, bob, "libfoo", name, strings.NewReader("x"))
		if err != nil || a.Filename != "file" {
			t.Errorf("filename %q became %q, %v", name, a.Filename, err)
		}
	}
	long := strings.Repeat("가", 100)
	a, err := s.Upload(ctx, bob, "libfoo", long, strings.NewReader("x"))
	if err != nil || len(a.Filename) > maxFilenameBytes || !strings.HasPrefix(long, a.Filename) {
		t.Fatalf("long filename %q (%d bytes), %v", a.Filename, len(a.Filename), err)
	}
}

func TestDelete(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	a, _ := s.Upload(ctx, bob, "libfoo", "a.txt", strings.NewReader("x"))
	if err := s.Delete(ctx, carol, a.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-uploader delete: %v", err)
	}
	if err := s.Delete(ctx, bob, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Open(ctx, carol, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted attachment downloadable: %v", err)
	}
	b, _ := s.Upload(ctx, bob, "libfoo", "b.txt", strings.NewReader("y"))
	if err := s.Delete(ctx, admin, b.ID); err != nil {
		t.Fatalf("admin delete: %v", err)
	}
}

func TestInfo(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	png, err := s.Upload(ctx, bob, "libfoo", "shot.png", strings.NewReader("\x89PNG\r\n\x1a\n0000"))
	if err != nil {
		t.Fatal(err)
	}
	log, _ := s.Upload(ctx, bob, "libfoo", "crash.log", strings.NewReader("panic"))
	tests := []struct {
		name    string
		id      string
		want    Attachment
		wantErr error
	}{
		{"image", png.ID, png, nil},
		{"other type", log.ID, log, nil},
		{"unknown", "01JD0000000000000000000NONE", Attachment{}, ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.Info(ctx, carol, tt.id)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
	if !png.Inline || log.Inline {
		t.Fatalf("inline: png %v, log %v", png.Inline, log.Inline)
	}
	if err := s.Delete(ctx, bob, log.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Info(ctx, carol, log.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted attachment described: %v", err)
	}
}
