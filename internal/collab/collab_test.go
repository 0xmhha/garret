package collab

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/reearth/ygo/crdt"
	ysync "github.com/reearth/ygo/sync"

	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/inbox"
	"github.com/0xmhha/garret/internal/realtime"
	"github.com/0xmhha/garret/internal/store"
)

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

type nopEvents struct{}

func (nopEvents) Publish(realtime.Event) {}

var (
	bob   = auth.User{ID: "01JD000000000000000000BOB0", Username: "bob", Role: auth.RoleMember}
	carol = auth.User{ID: "01JD00000000000000000CAROL", Username: "carol", Role: auth.RoleMember}
)

const docID = "01JD000000000000000000DOC1"

// fakeConn records what the server sends to one client.
type fakeConn struct {
	mu     sync.Mutex
	msgs   [][]byte
	closed bool
	got    chan struct{}
}

func newConn() *fakeConn { return &fakeConn{got: make(chan struct{}, 64)} }

func (c *fakeConn) Send(m []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("closed")
	}
	c.msgs = append(c.msgs, append([]byte(nil), m...))
	select {
	case c.got <- struct{}{}:
	default:
	}
	return nil
}

func (c *fakeConn) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
}

func (c *fakeConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *fakeConn) all() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]byte(nil), c.msgs...)
}

// wait blocks until the connection has received at least n messages.
func (c *fakeConn) wait(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for len(c.all()) < n {
		select {
		case <-c.got:
		case <-deadline:
			t.Fatalf("got %d messages, want %d", len(c.all()), n)
		}
	}
}

type env struct {
	db  *store.DB
	svc *Service
}

func newEnv(ctx context.Context, t *testing.T, path string) *env {
	t.Helper()
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ids, _ := id.NewGenerator(wallClock{}, rand.Reader)
	svc := New(Config{DB: db, IDs: ids, Clock: wallClock{}, Notifier: inbox.NewNotifier(ids, wallClock{}), Events: nopEvents{},
		Log: slog.New(slog.DiscardHandler), Quiet: 50 * time.Millisecond, Grace: 50 * time.Millisecond})
	run, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { svc.Run(run); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return &env{db: db, svc: svc}
}

func seed(ctx context.Context, t *testing.T, db *store.DB) {
	t.Helper()
	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			"INSERT INTO users VALUES ('" + bob.ID + "', 'bob', 'B', 'h', 'member', 1, NULL)",
			"INSERT INTO users VALUES ('" + carol.ID + "', 'carol', 'C', 'h', 'member', 1, NULL)",
			"INSERT INTO projects VALUES ('01JD0000000000000000PROJ01', 'libfoo', '', '" + bob.ID + "', 1, 1, NULL)",
			"INSERT INTO collab_documents VALUES ('" + docID + "', '01JD0000000000000000PROJ01', 'discussion_body', 1, 1, 1)",
			"INSERT INTO discussions VALUES ('01JD0000000000000000DISC01', '01JD0000000000000000PROJ01', 'Plan', '" + bob.ID + "', '" + docID + "', 1, 1, NULL)",
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// client is an in-process Yjs peer.
type client struct {
	doc  *crdt.Doc
	text *crdt.YText
}

func newClient() *client {
	d := crdt.New()
	return &client{doc: d, text: d.GetText(TextName)}
}

// edit inserts s at the end and returns the framed update message.
func (c *client) edit(s string) []byte {
	var update []byte
	unsub := c.doc.OnUpdate(func(u []byte, _ any) { update = append([]byte(nil), u...) })
	c.doc.Transact(func(tx *crdt.Transaction) { c.text.Insert(tx, c.text.Len(), s, nil) })
	unsub()
	return frame(ysync.EncodeUpdate(update))
}

// apply applies every sync message in msgs to the client.
func (c *client) apply(t *testing.T, msgs [][]byte) {
	t.Helper()
	for _, m := range msgs {
		if len(m) == 0 || m[0] != messageSync {
			continue
		}
		if _, err := ysync.ApplySyncMessage(c.doc, m[1:], "server"); err != nil {
			t.Fatal(err)
		}
	}
}

func countRows(ctx context.Context, t *testing.T, db *store.DB, table string) int {
	t.Helper()
	var n int
	_ = db.ReadTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n)
	})
	return n
}

func TestEditsAreStoredAndBroadcast(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garret.db")
	e := newEnv(context.Background(), t, path)
	ctx := context.Background()
	seed(ctx, t, e.db)
	ca, cb := newConn(), newConn()
	sa, err := e.svc.Join(ctx, bob, "libfoo", docID, 1, ca)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := e.svc.Join(ctx, carol, "libfoo", docID, 1, cb)
	if err != nil {
		t.Fatal(err)
	}
	ca.wait(t, 1) // the server's sync step 1
	a, b := newClient(), newClient()
	if err := sa.Receive(ctx, a.edit("hello @carol")); err != nil {
		t.Fatal(err)
	}
	cb.wait(t, 2)
	b.apply(t, cb.all())
	if got := b.text.ToString(); got != "hello @carol" {
		t.Fatalf("peer text %q", got)
	}
	if n := countRows(ctx, t, e.db, "collab_updates"); n != 1 {
		t.Fatalf("%d updates stored", n)
	}
	// Quiet period passes: a checkpoint with the text and a mention.
	deadline := time.Now().Add(5 * time.Second)
	for countRows(ctx, t, e.db, "collab_checkpoints") == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond) // polling a database row the owner writes on its own timer
	}
	text, err := e.svc.Text(ctx, docID)
	if err != nil || text != "hello @carol" {
		t.Fatalf("checkpoint text %q, %v", text, err)
	}
	if n := countRows(ctx, t, e.db, "notifications"); n != 1 {
		t.Fatalf("%d notifications, want one mention", n)
	}
	sa.Leave()
	sb.Leave()
}

func TestRejectsBadUpdates(t *testing.T) {
	e := newEnv(context.Background(), t, filepath.Join(t.TempDir(), "garret.db"))
	ctx := context.Background()
	seed(ctx, t, e.db)
	c := newConn()
	s, err := e.svc.Join(ctx, bob, "libfoo", docID, 1, c)
	if err != nil {
		t.Fatal(err)
	}
	garbage := frame(ysync.EncodeUpdate([]byte{0xff, 0xff, 0xff, 0xff}))
	_ = s.Receive(ctx, garbage)
	deadline := time.Now().Add(5 * time.Second)
	for !c.isClosed() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond) // waiting for the owner goroutine to close the connection
	}
	if !c.isClosed() {
		t.Fatal("connection not closed after a malformed update")
	}
	if n := countRows(ctx, t, e.db, "collab_updates"); n != 0 {
		t.Fatalf("%d malformed updates stored", n)
	}
	c2 := newConn()
	s2, _ := e.svc.Join(ctx, bob, "libfoo", docID, 1, c2)
	big := frame(ysync.EncodeUpdate(make([]byte, MaxUpdateBytes+1)))
	if err := s2.Receive(ctx, big); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized update: %v", err)
	}
}

func TestJoinChecksDocument(t *testing.T) {
	e := newEnv(context.Background(), t, filepath.Join(t.TempDir(), "garret.db"))
	ctx := context.Background()
	seed(ctx, t, e.db)
	if _, err := e.svc.Join(ctx, bob, "other", docID, 1, newConn()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong project: %v", err)
	}
	if _, err := e.svc.Join(ctx, bob, "libfoo", "01JD0000000000000000NOPE00", 1, newConn()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing document: %v", err)
	}
}

func TestReloadFromCheckpointAndUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garret.db")
	ctx := context.Background()
	func() {
		e := newEnv(ctx, t, path)
		seed(ctx, t, e.db)
		c := newConn()
		s, err := e.svc.Join(ctx, bob, "libfoo", docID, 1, c)
		if err != nil {
			t.Fatal(err)
		}
		cl := newClient()
		_ = s.Receive(ctx, cl.edit("one "))
		deadline := time.Now().Add(5 * time.Second)
		for countRows(ctx, t, e.db, "collab_checkpoints") == 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond) // waiting for the quiet-period checkpoint
		}
		_ = s.Receive(ctx, cl.edit("two"))
		for countRows(ctx, t, e.db, "collab_updates") < 2 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond) // waiting for the second update to be stored
		}
		s.Leave()
	}()
	e := newEnv(context.Background(), t, path)
	c := newConn()
	if _, err := e.svc.Join(ctx, carol, "libfoo", docID, 1, c); err != nil {
		t.Fatal(err)
	}
	c.wait(t, 1)
	cl := newClient()
	// Ask the server for everything: send step 1 from an empty client.
	s, _ := e.svc.Join(ctx, carol, "libfoo", docID, 1, newConn())
	_ = s
	reply := newConn()
	s3, _ := e.svc.Join(ctx, carol, "libfoo", docID, 1, reply)
	if err := s3.Receive(ctx, frame(ysync.EncodeSyncStep1(cl.doc))); err != nil {
		t.Fatal(err)
	}
	reply.wait(t, 2)
	cl.apply(t, reply.all())
	if got := cl.text.ToString(); got != "one two" {
		t.Fatalf("reloaded text %q", got)
	}
}

func TestAwarenessIsRelayedNotStored(t *testing.T) {
	e := newEnv(context.Background(), t, filepath.Join(t.TempDir(), "garret.db"))
	ctx := context.Background()
	seed(ctx, t, e.db)
	ca, cb := newConn(), newConn()
	sa, _ := e.svc.Join(ctx, bob, "libfoo", docID, 1, ca)
	if _, err := e.svc.Join(ctx, carol, "libfoo", docID, 1, cb); err != nil {
		t.Fatal(err)
	}
	cb.wait(t, 1)
	if err := sa.Receive(ctx, []byte{messageAwareness, 1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	cb.wait(t, 2)
	last := cb.all()[len(cb.all())-1]
	if !strings.HasPrefix(string(last), string([]byte{messageAwareness, 1, 2, 3})) {
		t.Fatalf("relayed %v", last)
	}
	if n := countRows(ctx, t, e.db, "collab_updates"); n != 0 {
		t.Fatal("awareness stored")
	}
}

// crashUpdate is an update that makes ygo v1.55.0 panic while it is
// parsed: a subdocument whose guid is not valid UTF-8. Before the fix it
// stopped the whole server process.
var crashUpdate = []byte{messageSync, 0x02, 0x0f,
	0x01, 0x01, 0x01, 0x00, 0x09, 0x01, 0x04, 'b', 'o', 'd', 'y', 0x01, 0xff, 0x7f, 0x00}

func TestPanickingUpdateClosesOnlyTheSender(t *testing.T) {
	ctx := context.Background()
	e := newEnv(ctx, t, filepath.Join(t.TempDir(), "garret.db"))
	seed(ctx, t, e.db)
	attacker, bystander := newConn(), newConn()
	sa, err := e.svc.Join(ctx, bob, "libfoo", docID, 1, attacker)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := e.svc.Join(ctx, carol, "libfoo", docID, 1, bystander)
	if err != nil {
		t.Fatal(err)
	}
	bystander.wait(t, 1)
	_ = sa.Receive(ctx, crashUpdate)
	deadline := time.Now().Add(5 * time.Second)
	for !attacker.isClosed() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond) // waiting for the owner goroutine to drop the sender
	}
	if !attacker.isClosed() {
		t.Fatal("sender of a panicking update not disconnected")
	}
	if n := countRows(ctx, t, e.db, "collab_updates"); n != 0 {
		t.Fatalf("%d rows stored for a panicking update", n)
	}
	// The document still works for everyone else.
	cl := newClient()
	if err := sb.Receive(ctx, cl.edit("still alive")); err != nil {
		t.Fatal(err)
	}
	for countRows(ctx, t, e.db, "collab_updates") == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond) // waiting for the bystander's update to be stored
	}
	if n := countRows(ctx, t, e.db, "collab_updates"); n != 1 {
		t.Fatalf("bystander's update not stored: %d rows", n)
	}
}

func TestDeletedDiscussionCannotBeJoined(t *testing.T) {
	ctx := context.Background()
	e := newEnv(ctx, t, filepath.Join(t.TempDir(), "garret.db"))
	seed(ctx, t, e.db)
	if err := e.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE discussions SET deleted_at = 5")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Join(ctx, bob, "libfoo", docID, 1, newConn()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("joined a deleted discussion: %v", err)
	}
}

func TestJoinDuringUnloadIsServed(t *testing.T) {
	ctx := context.Background()
	e := newEnv(ctx, t, filepath.Join(t.TempDir(), "garret.db"))
	seed(ctx, t, e.db)
	for range 50 {
		c := newConn()
		s, err := e.svc.Join(ctx, bob, "libfoo", docID, 1, c)
		if err != nil {
			t.Fatal(err)
		}
		c.wait(t, 1) // every join gets sync step 1, even right as the owner unloads
		s.Leave()
		time.Sleep(time.Duration(len(c.all())) * 45 * time.Millisecond) // land near the 50 ms grace expiry
	}
}

func TestRebuildErasesHistory(t *testing.T) {
	ctx := context.Background()
	e := newEnv(ctx, t, filepath.Join(t.TempDir(), "garret.db"))
	seed(ctx, t, e.db)
	c := newConn()
	s, err := e.svc.Join(ctx, bob, "libfoo", docID, 1, c)
	if err != nil {
		t.Fatal(err)
	}
	cl := newClient()
	_ = s.Receive(ctx, cl.edit("public part. SECRET exploit details"))
	deadline := time.Now().Add(5 * time.Second)
	for countRows(ctx, t, e.db, "collab_updates") == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond) // waiting for the update to be stored
	}
	called := false
	gen, err := e.svc.Rebuild(ctx, docID, "public part.", func(*sql.Tx) error { called = true; return nil })
	if err != nil || gen != 2 || !called {
		t.Fatalf("Rebuild = %d, %v, callback %v", gen, err, called)
	}
	if !c.isClosed() {
		t.Fatal("connected client not told to reload")
	}
	if last := c.all()[len(c.all())-1]; len(last) != 1 || last[0] != messageReload {
		t.Fatalf("last message %v, want a reload", last)
	}
	var leaked int
	_ = e.db.ReadTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM collab_updates) + (SELECT count(*) FROM collab_checkpoints WHERE CAST(state AS TEXT) LIKE '%SECRET%' OR text LIKE '%SECRET%')").Scan(&leaked)
	})
	if leaked != 0 {
		t.Fatalf("%d rows still hold the erased text or old updates", leaked)
	}
	if text, _ := e.svc.Text(ctx, docID); text != "public part." {
		t.Fatalf("text after rebuild %q", text)
	}
	if _, err := e.svc.Join(ctx, bob, "libfoo", docID, 1, newConn()); !errors.Is(err, ErrStale) {
		t.Fatalf("join with the old generation: %v", err)
	}
	fresh := newConn()
	if _, err := e.svc.Join(ctx, bob, "libfoo", docID, 2, fresh); err != nil {
		t.Fatal(err)
	}
	fresh.wait(t, 1)
	nc := newClient()
	sess, _ := e.svc.Join(ctx, bob, "libfoo", docID, 2, newConn())
	reply := newConn()
	sess3, _ := e.svc.Join(ctx, bob, "libfoo", docID, 2, reply)
	_ = sess
	if err := sess3.Receive(ctx, frame(ysync.EncodeSyncStep1(nc.doc))); err != nil {
		t.Fatal(err)
	}
	reply.wait(t, 2)
	nc.apply(t, reply.all())
	if got := nc.text.ToString(); got != "public part." {
		t.Fatalf("a new client sees %q", got)
	}
}

func TestRebuildCallbackFailureChangesNothing(t *testing.T) {
	ctx := context.Background()
	e := newEnv(ctx, t, filepath.Join(t.TempDir(), "garret.db"))
	seed(ctx, t, e.db)
	errStop := errors.New("stop")
	if _, err := e.svc.Rebuild(ctx, docID, "x", func(*sql.Tx) error { return errStop }); !errors.Is(err, errStop) {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.svc.Join(ctx, bob, "libfoo", docID, 1, newConn()); err != nil {
		t.Fatalf("generation changed after a failed rebuild: %v", err)
	}
}

func TestGenerationAndCloseUser(t *testing.T) {
	ctx := context.Background()
	e := newEnv(ctx, t, filepath.Join(t.TempDir(), "garret.db"))
	seed(ctx, t, e.db)
	if g, err := e.svc.Generation(ctx, "libfoo", docID); err != nil || g != 1 {
		t.Fatalf("Generation = %d, %v", g, err)
	}
	if _, err := e.svc.Generation(ctx, "other", docID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong project: %v", err)
	}
	cb, cc := newConn(), newConn()
	if _, err := e.svc.Join(ctx, bob, "libfoo", docID, 1, cb); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Join(ctx, carol, "libfoo", docID, 1, cc); err != nil {
		t.Fatal(err)
	}
	cb.wait(t, 1)
	cc.wait(t, 1)
	e.svc.CloseUser(bob.ID)
	deadline := time.Now().Add(5 * time.Second)
	for !cb.isClosed() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond) // waiting for the owner goroutine
	}
	if !cb.isClosed() || cc.isClosed() {
		t.Fatalf("bob closed %v, carol closed %v", cb.isClosed(), cc.isClosed())
	}
}
