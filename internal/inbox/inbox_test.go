package inbox

import (
	"context"
	"crypto/rand"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/store"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Millisecond)
	return c.now
}

var (
	bob   = auth.User{ID: "01JD000000000000000000BOB0", Username: "bob"}
	carol = auth.User{ID: "01JD00000000000000000CAROL", Username: "carol"}
)

func setup(t *testing.T) (*Service, *store.DB, *testClock) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "garret.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	err = db.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, u := range []auth.User{bob, carol} {
			if _, err := tx.ExecContext(ctx, "INSERT INTO users VALUES (?, ?, 'N', 'h', 'member', 1, NULL)", u.ID, u.Username); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO projects VALUES ('P1', 'p', '', ?, 1, 1)", bob.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	c := &testClock{now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
	ids, _ := id.NewGenerator(c, rand.Reader)
	return NewService(db, NewNotifier(ids, c), c), db, c
}

func notify(t *testing.T, s *Service, db *store.DB, user string, kind Kind, subject string) {
	t.Helper()
	err := db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		return s.Notifier().Notify(context.Background(), tx, Notification{UserID: user, ProjectID: "P1", Kind: kind, SubjectKind: SubjectComment, SubjectID: subject})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestListAndMarkRead(t *testing.T) {
	s, db, _ := setup(t)
	ctx := context.Background()
	notify(t, s, db, bob.ID, KindMention, "C1")
	notify(t, s, db, bob.ID, KindReplyToMe, "C2")
	notify(t, s, db, carol.ID, KindMention, "C3")
	list, err := s.List(ctx, bob, false, "", 10)
	if err != nil || len(list) != 2 || list[0].SubjectID != "C2" {
		t.Fatalf("List = %+v, %v (want newest first, only bob's)", list, err)
	}
	if err := s.MarkRead(ctx, carol, []string{list[0].ID}); err != nil {
		t.Fatal(err)
	}
	if unread, _ := s.List(ctx, bob, true, "", 10); len(unread) != 2 {
		t.Fatal("another user marked bob's notification as read")
	}
	if err := s.MarkRead(ctx, bob, []string{list[0].ID}); err != nil {
		t.Fatal(err)
	}
	unread, _ := s.List(ctx, bob, true, "", 10)
	if len(unread) != 1 || unread[0].SubjectID != "C1" {
		t.Fatalf("unread %+v", unread)
	}
	page, _ := s.List(ctx, bob, false, list[0].ID, 10)
	if len(page) != 1 || page[0].SubjectID != "C1" {
		t.Fatalf("cursor page %+v", page)
	}
}

func TestNotifyRejectsBadInput(t *testing.T) {
	s, db, _ := setup(t)
	err := db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		return s.Notifier().Notify(context.Background(), tx, Notification{UserID: bob.ID, ProjectID: "P1", Kind: Kind("spam"), SubjectKind: SubjectComment, SubjectID: "C"})
	})
	if err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func TestSweep(t *testing.T) {
	s, db, c := setup(t)
	notify(t, s, db, bob.ID, KindMention, "old")
	c.mu.Lock()
	c.now = c.now.Add(Retention + time.Hour)
	c.mu.Unlock()
	notify(t, s, db, bob.ID, KindMention, "new")
	n, err := s.Sweep(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("swept %d, %v", n, err)
	}
	list, _ := s.List(context.Background(), bob, false, "", 10)
	if len(list) != 1 || list[0].SubjectID != "new" {
		t.Fatalf("left %+v", list)
	}
}
