package collab

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/reearth/ygo/crdt"
	ysync "github.com/reearth/ygo/sync"

	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/clock"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/inbox"
	"github.com/0xmhha/garret/internal/realtime"
	"github.com/0xmhha/garret/internal/store"
	"github.com/0xmhha/garret/internal/store/sqlcgen"
)

// Service errors.
var (
	ErrNotFound = errors.New("collab: document not found")
	ErrTooLarge = errors.New("collab: update too large")
	ErrClosed   = errors.New("collab: session closed")
	// errPanic marks a panic inside the Yjs library turned into an error.
	errPanic = errors.New("collab: Yjs library panicked")
)

// guard runs fn and turns a panic into an error. Every call into ygo goes
// through it: ygo v1.55.0 can panic on crafted input (a subdocument guid
// that is not UTF-8), and an unrecovered panic in an owner goroutine would
// stop the whole server.
func guard(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", errPanic, r)
		}
	}()
	return fn()
}

// TextName is the name of the Y.Text that holds a document's content. The
// browser binds the same name to its editor.
const TextName = "body"

// MaxUpdateBytes is the size limit of one update (data.md, Q5).
const MaxUpdateBytes = 1 << 20

// Timings (services.md, Q6).
const (
	DefaultQuiet = 30 * time.Second
	DefaultGrace = 60 * time.Second
	inboxSize    = 64
)

// Outer message types of the y-websocket framing.
const (
	messageSync      byte = 0
	messageAwareness byte = 1
)

var mentionRE = regexp.MustCompile(`(?:^|[^A-Za-z0-9_@-])@([a-z0-9][a-z0-9_-]{1,31})\b`)

// Conn is one client connection as the transport provides it. Send must not
// block for long; a transport whose buffer is full closes the connection.
type Conn interface {
	Send(msg []byte) error
	Close()
}

// Publisher receives events.
type Publisher interface {
	Publish(realtime.Event)
}

// Config holds the Service's dependencies.
type Config struct {
	DB       *store.DB
	IDs      *id.Generator
	Clock    clock.Clock
	Notifier *inbox.Notifier
	Events   Publisher
	Log      *slog.Logger
	Quiet    time.Duration // DefaultQuiet in production
	Grace    time.Duration // DefaultGrace in production
}

// Service loads collaborative documents on demand and serves their
// clients. It is safe for concurrent use. Run must be running.
type Service struct {
	cfg Config

	mu     sync.Mutex
	owners map[string]*owner
	runCtx context.Context
	wg     sync.WaitGroup
	ready  chan struct{}
}

// New returns a Service. Start it with Run.
func New(cfg Config) *Service {
	return &Service{cfg: cfg, owners: map[string]*owner{}, ready: make(chan struct{})}
}

// Run serves documents until ctx is done, then waits for every owner to
// write its final checkpoint and stop.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.runCtx = ctx
	s.mu.Unlock()
	close(s.ready)
	<-ctx.Done()
	s.wg.Wait()
}

// Session is one client's place in a document.
type Session struct {
	owner *owner
	conn  Conn
	user  string
	once  sync.Once
}

// Join connects conn to the document docID of projectName for actor. The
// server sends sync step 1 first.
func (s *Service) Join(ctx context.Context, actor auth.User, projectName, docID string, conn Conn) (*Session, error) {
	if err := s.checkDocument(ctx, projectName, docID); err != nil {
		return nil, err
	}
	for {
		o, queued, sess, err := s.ownerWithJoin(ctx, docID, actor.ID, conn)
		if err != nil {
			return nil, err
		}
		if queued {
			return sess, nil
		}
		// The owner's queue is full, so it is busy and not unloading; wait.
		select {
		case o.cmds <- command{join: sess}:
			return sess, nil
		case <-o.done:
			continue // the owner stopped; load the document again
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Receive hands one client message to the document's owner. It returns
// ErrTooLarge for an update over the limit and ErrClosed once the session
// has ended.
func (sess *Session) Receive(ctx context.Context, msg []byte) error {
	if len(msg) > 1 && msg[0] == messageSync {
		var payload []byte
		err := guard(func() error {
			var err error
			_, payload, err = ysync.ReadSyncMessage(msg[1:])
			return err
		})
		if err == nil && len(payload) > MaxUpdateBytes {
			sess.conn.Close()
			sess.Leave()
			return ErrTooLarge
		}
	}
	select {
	case sess.owner.cmds <- command{from: sess, msg: msg}:
		return nil
	case <-sess.owner.done:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Leave ends the session. Calling it more than once has no effect.
func (sess *Session) Leave() {
	sess.once.Do(func() {
		select {
		case sess.owner.cmds <- command{leave: sess}:
		case <-sess.owner.done:
		}
	})
}

// Text returns the latest checkpoint text of docID, "" before the first
// checkpoint.
func (s *Service) Text(ctx context.Context, docID string) (string, error) {
	var cp sqlcgen.CollabCheckpoint
	err := s.cfg.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		cp, err = sqlcgen.New(tx).LatestCheckpoint(ctx, docID)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return cp.Text, err
}

func (s *Service) checkDocument(ctx context.Context, projectName, docID string) error {
	return s.cfg.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		d, err := q.GetCollabDocument(ctx, docID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		p, err := q.GetProjectByID(ctx, d.ProjectID)
		if err != nil {
			return err
		}
		if p.Name != projectName {
			return ErrNotFound
		}
		if d.Kind == "discussion_body" {
			disc, err := q.GetDiscussionByBodyDoc(ctx, docID)
			if err != nil || disc.DeletedAt.Valid {
				return ErrNotFound
			}
		}
		return nil
	})
}

// ownerWithJoin finds or loads the owner of docID and, under the same lock
// the owner takes to unload, tries to queue the join. A join queued here is
// always served: the owner unloads only with an empty queue.
func (s *Service) ownerWithJoin(ctx context.Context, docID, user string, conn Conn) (*owner, bool, *Session, error) {
	o, err := s.owner(ctx, docID)
	if err != nil {
		return nil, false, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := &Session{owner: o, conn: conn, user: user}
	if s.owners[o.docID] != o {
		return s.ownerWithJoinRetry(ctx, docID, user, conn)
	}
	select {
	case o.cmds <- command{join: sess}:
		return o, true, sess, nil
	default:
		return o, false, sess, nil
	}
}

// ownerWithJoinRetry handles an owner that unloaded between lookup and
// lock; it is called with s.mu held and releases nothing.
func (s *Service) ownerWithJoinRetry(ctx context.Context, docID, user string, conn Conn) (*owner, bool, *Session, error) {
	if s.runCtx.Err() != nil {
		return nil, false, nil, ErrClosed
	}
	o, err := s.load(ctx, docID)
	if err != nil {
		return nil, false, nil, err
	}
	s.owners[docID] = o
	s.wg.Go(func() { o.run(s.runCtx) })
	sess := &Session{owner: o, conn: conn, user: user}
	o.cmds <- command{join: sess} // a new owner's queue is empty
	return o, true, sess, nil
}

// owner returns the running owner of docID, loading the document if needed.
func (s *Service) owner(ctx context.Context, docID string) (*owner, error) {
	select {
	case <-s.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runCtx.Err() != nil {
		return nil, ErrClosed
	}
	if o := s.owners[docID]; o != nil {
		return o, nil
	}
	o, err := s.load(ctx, docID)
	if err != nil {
		return nil, err
	}
	s.owners[docID] = o
	s.wg.Go(func() { o.run(s.runCtx) })
	return o, nil
}

// remove drops o from the map if it is still the registered owner.
func (s *Service) remove(o *owner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owners[o.docID] == o {
		delete(s.owners, o.docID)
	}
}

// tryUnload removes o from the map if it has no sessions and nothing
// queued, under the lock that joins queue under. It reports whether o may
// stop.
func (s *Service) tryUnload(o *owner) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(o.sessions) > 0 || len(o.cmds) > 0 {
		return false
	}
	if s.owners[o.docID] == o {
		delete(s.owners, o.docID)
	}
	return true
}

func (s *Service) load(ctx context.Context, docID string) (*owner, error) {
	doc := crdt.New()
	o := &owner{svc: s, docID: docID, doc: doc, text: doc.GetText(TextName),
		cmds: make(chan command, inboxSize), done: make(chan struct{}), sessions: map[*Session]bool{}, contributors: map[string]bool{}}
	err := s.cfg.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		d, err := q.GetCollabDocument(ctx, docID)
		if err != nil {
			return err
		}
		o.projectID, o.kind = d.ProjectID, d.Kind
		cp, err := q.LatestCheckpoint(ctx, docID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		default:
			if err := guard(func() error { return doc.ApplyUpdate(cp.State) }); err != nil {
				return fmt.Errorf("collab: load checkpoint: %w", err)
			}
			o.lastSeq, o.checkpointSeq, o.lastText = cp.UpToSeq, cp.UpToSeq, cp.Text
		}
		updates, err := q.ListUpdatesAfter(ctx, sqlcgen.ListUpdatesAfterParams{DocumentID: docID, Seq: o.lastSeq})
		if err != nil {
			return err
		}
		for _, u := range updates {
			if err := guard(func() error { return doc.ApplyUpdate(u.Payload) }); err != nil {
				s.cfg.Log.Error("skipping an update that does not apply", "document", docID, "seq", u.Seq, "err", err)
			}
			o.lastSeq = u.Seq
			o.contributors[u.UserID] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return o, nil
}

// command is one request to an owner. Exactly one field group is set.
type command struct {
	join  *Session
	leave *Session
	from  *Session
	msg   []byte
}

// owner is the only goroutine that touches its document and writes its
// rows, so sequence numbers need no further locking.
type owner struct {
	svc       *Service
	docID     string
	projectID string
	kind      string
	doc       *crdt.Doc
	text      *crdt.YText // obtained once at load: GetText inside Transact deadlocks

	cmds chan command
	done chan struct{}

	sessions      map[*Session]bool
	lastSeq       int64
	checkpointSeq int64
	lastText      string
	contributors  map[string]bool
}

func (o *owner) run(ctx context.Context) {
	defer close(o.done)
	defer o.svc.remove(o)
	quiet := time.NewTimer(o.svc.cfg.Quiet)
	quiet.Stop()
	grace := time.NewTimer(o.svc.cfg.Grace)
	defer quiet.Stop()
	defer grace.Stop()
	for {
		select {
		case <-ctx.Done():
			o.checkpoint(context.WithoutCancel(ctx))
			for sess := range o.sessions {
				sess.conn.Close()
			}
			return
		case <-quiet.C:
			o.checkpoint(ctx)
		case <-grace.C:
			if o.svc.tryUnload(o) {
				o.checkpoint(ctx)
				return
			}
		case c := <-o.cmds:
			if o.process(ctx, c, grace) {
				quiet.Reset(o.svc.cfg.Quiet)
			}
		}
	}
}

// process runs one command and reports whether the document changed. A
// panic while handling a client's message drops that client only.
func (o *owner) process(ctx context.Context, c command, grace *time.Timer) (changed bool) {
	defer func() {
		if r := recover(); r != nil {
			o.svc.cfg.Log.Error("dropping a connection after a panic", "document", o.docID, "panic", r)
			if c.from != nil {
				o.drop(c.from)
			}
			if c.join != nil {
				o.drop(c.join)
			}
			changed = false
		}
	}()
	switch {
	case c.join != nil:
		o.sessions[c.join] = true
		grace.Stop()
		var step1 []byte
		if err := guard(func() error { step1 = ysync.EncodeSyncStep1(o.doc); return nil }); err != nil {
			o.drop(c.join)
			return false
		}
		o.send(c.join, frame(step1))
	case c.leave != nil:
		delete(o.sessions, c.leave)
		if len(o.sessions) == 0 {
			grace.Reset(o.svc.cfg.Grace)
		}
	case c.from != nil && o.sessions[c.from]:
		return o.handle(ctx, c.from, c.msg)
	}
	return false
}

// handle processes one client message and reports whether the document
// changed.
func (o *owner) handle(ctx context.Context, from *Session, msg []byte) bool {
	if len(msg) == 0 {
		o.drop(from)
		return false
	}
	switch msg[0] {
	case messageAwareness:
		for sess := range o.sessions {
			if sess != from {
				o.send(sess, msg)
			}
		}
		return false
	case messageSync:
	default:
		o.drop(from)
		return false
	}
	inner := msg[1:]
	var kind int
	var payload []byte
	if err := guard(func() error {
		var err error
		kind, payload, err = ysync.ReadSyncMessage(inner)
		return err
	}); err != nil {
		o.drop(from)
		return false
	}
	if kind == ysync.MsgSyncStep1 {
		var reply []byte
		err := guard(func() error {
			var err error
			reply, err = ysync.EncodeSyncStep2(o.doc, inner)
			return err
		})
		if err != nil {
			o.drop(from)
			return false
		}
		o.send(from, frame(reply))
		return false
	}
	return o.update(ctx, from, payload)
}

// update stores an update first, then applies and broadcasts it, so peers
// never see an update that is not stored (data.md 6.5).
func (o *owner) update(ctx context.Context, from *Session, update []byte) bool {
	if len(update) == 0 || len(update) > MaxUpdateBytes {
		o.drop(from)
		return false
	}
	if err := guard(func() error { _, err := crdt.EncodeStateVectorFromUpdate(update); return err }); err != nil {
		o.drop(from) // malformed or crafted to panic: nothing is stored
		return false
	}
	var seq int64
	err := o.svc.cfg.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		var err error
		if seq, err = q.AllocateUpdateSeq(ctx, o.docID); err != nil {
			return err
		}
		return q.InsertCollabUpdate(ctx, sqlcgen.InsertCollabUpdateParams{DocumentID: o.docID, Seq: seq, UserID: from.user,
			ReceivedAt: o.svc.cfg.Clock.Now().UnixMilli(), Payload: update})
	})
	if err != nil {
		o.svc.cfg.Log.Error("storing a collaborative update failed", "document", o.docID, "err", err)
		o.drop(from)
		return false
	}
	if err := guard(func() error { return o.doc.ApplyUpdate(update) }); err != nil {
		o.svc.cfg.Log.Error("a stored update did not apply", "document", o.docID, "seq", seq, "err", err)
	}
	o.lastSeq = seq
	o.contributors[from.user] = true
	out := frame(ysync.EncodeUpdate(update))
	for sess := range o.sessions {
		if sess != from {
			o.send(sess, out)
		}
	}
	return true
}

// checkpoint writes the full state and text when updates arrived since the
// last checkpoint, and notifies users newly mentioned in the text.
func (o *owner) checkpoint(ctx context.Context) {
	if o.lastSeq == o.checkpointSeq {
		return
	}
	var text string
	var state []byte
	if err := guard(func() error { text, state = o.text.ToString(), o.doc.EncodeStateAsUpdate(); return nil }); err != nil {
		o.svc.cfg.Log.Error("reading the document for a checkpoint failed", "document", o.docID, "err", err)
		return
	}
	contributors := make([]string, 0, len(o.contributors))
	for u := range o.contributors {
		contributors = append(contributors, u)
	}
	slices.Sort(contributors)
	encoded, err := json.Marshal(contributors)
	if err != nil {
		o.svc.cfg.Log.Error("encoding contributors failed", "err", err)
		return
	}
	var notified []string
	err = o.svc.cfg.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		if err := q.InsertCheckpoint(ctx, sqlcgen.InsertCheckpointParams{DocumentID: o.docID, UpToSeq: o.lastSeq,
			State: state, Text: text, Contributors: string(encoded),
			CreatedAt: o.svc.cfg.Clock.Now().UnixMilli()}); err != nil {
			return err
		}
		var err error
		notified, err = o.notifyMentions(ctx, tx, q, text)
		return err
	})
	if err != nil {
		o.svc.cfg.Log.Error("writing a checkpoint failed", "document", o.docID, "err", err)
		return
	}
	o.checkpointSeq, o.lastText = o.lastSeq, text
	o.contributors = map[string]bool{}
	for _, u := range notified {
		o.svc.cfg.Events.Publish(realtime.Event{Type: "notification.created", User: u})
	}
}

func (o *owner) notifyMentions(ctx context.Context, tx *sql.Tx, q *sqlcgen.Queries, text string) ([]string, error) {
	names := mentions(text)
	old := mentions(o.lastText)
	names = slices.DeleteFunc(names, func(n string) bool { return slices.Contains(old, n) })
	if len(names) == 0 {
		return nil, nil
	}
	subjectKind, subjectID, err := o.subject(ctx, q)
	if err != nil {
		return nil, err
	}
	users, err := q.ListEnabledUsersByName(ctx, names)
	if err != nil {
		return nil, err
	}
	var notified []string
	for _, u := range users {
		// A contributor since the last checkpoint may have written the
		// mention; users are never notified of their own edits.
		if o.contributors[u.ID] {
			continue
		}
		if err := o.svc.cfg.Notifier.Notify(ctx, tx, inbox.Notification{UserID: u.ID, ProjectID: o.projectID,
			Kind: inbox.KindMention, SubjectKind: subjectKind, SubjectID: subjectID}); err != nil {
			return nil, err
		}
		notified = append(notified, u.ID)
	}
	return notified, nil
}

func (o *owner) subject(ctx context.Context, q *sqlcgen.Queries) (inbox.SubjectKind, string, error) {
	if o.kind == "review_description" {
		r, err := q.GetReviewByDescriptionDoc(ctx, o.docID)
		return inbox.SubjectReview, r.ID, err
	}
	d, err := q.GetDiscussionByBodyDoc(ctx, o.docID)
	return inbox.SubjectDiscussion, d.ID, err
}

func (o *owner) send(sess *Session, msg []byte) {
	if err := sess.conn.Send(msg); err != nil {
		o.drop(sess)
	}
}

func (o *owner) drop(sess *Session) {
	sess.conn.Close()
	delete(o.sessions, sess)
}

func frame(inner []byte) []byte {
	return append([]byte{messageSync}, inner...)
}

func mentions(text string) []string {
	var out []string
	for _, m := range mentionRE.FindAllStringSubmatch(text, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}
