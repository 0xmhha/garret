package inbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/clock"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/store"
	"github.com/0xmhha/garret/internal/store/sqlcgen"
)

// ErrInvalid is returned for a notification of an unknown kind.
var ErrInvalid = errors.New("inbox: invalid notification")

// Kind is why a user is notified.
type Kind string

// Notification kinds (data.md, notifications).
const (
	KindMention            Kind = "mention"
	KindCommentOnMyReview  Kind = "comment_on_my_review"
	KindReplyToMe          Kind = "reply_to_me"
	KindVerdictOnMyReview  Kind = "verdict_on_my_review"
	KindRevisionOnReviewed Kind = "revision_on_reviewed"
)

// SubjectKind is what a notification points at.
type SubjectKind string

// Subject kinds.
const (
	SubjectComment    SubjectKind = "comment"
	SubjectReview     SubjectKind = "review"
	SubjectDiscussion SubjectKind = "discussion"
	SubjectRevision   SubjectKind = "revision"
)

var kinds = map[Kind]bool{KindMention: true, KindCommentOnMyReview: true, KindReplyToMe: true,
	KindVerdictOnMyReview: true, KindRevisionOnReviewed: true}

var subjects = map[SubjectKind]bool{SubjectComment: true, SubjectReview: true, SubjectDiscussion: true, SubjectRevision: true}

// Retention is how long notifications are kept (data.md, Q2).
const Retention = 90 * 24 * time.Hour

const maxPage = 100

// Notification is one notification.
type Notification struct {
	ID          string
	UserID      string
	ProjectID   string
	Kind        Kind
	SubjectKind SubjectKind
	SubjectID   string
	CreatedAt   time.Time
	ReadAt      *time.Time
	// Where the notification points, filled by List: the project's name and
	// the review number or discussion ID its subject belongs to.
	Project    string
	Review     int64
	Discussion string
}

// Notifier adds notifications inside the caller's transaction.
type Notifier struct {
	ids   *id.Generator
	clock clock.Clock
}

// NewNotifier returns a Notifier.
func NewNotifier(ids *id.Generator, c clock.Clock) *Notifier { return &Notifier{ids: ids, clock: c} }

// Notify inserts note inside tx. Callers never notify a
// user of their own action; Notify does not check that, since it does not
// know the actor.
func (n *Notifier) Notify(ctx context.Context, tx *sql.Tx, note Notification) error {
	if !kinds[note.Kind] || !subjects[note.SubjectKind] || note.UserID == "" || note.SubjectID == "" {
		return fmt.Errorf("%w: %q/%q", ErrInvalid, note.Kind, note.SubjectKind)
	}
	nid, err := n.ids.New()
	if err != nil {
		return err
	}
	return sqlcgen.New(tx).InsertNotification(ctx, sqlcgen.InsertNotificationParams{
		ID: nid, UserID: note.UserID, ProjectID: note.ProjectID, Kind: string(note.Kind),
		SubjectKind: string(note.SubjectKind), SubjectID: note.SubjectID, CreatedAt: n.clock.Now().UnixMilli()})
}

// Service lists and marks notifications. It is safe for concurrent use.
type Service struct {
	db       *store.DB
	notifier *Notifier
	clock    clock.Clock
}

// NewService returns an inbox Service.
func NewService(db *store.DB, n *Notifier, c clock.Clock) *Service {
	return &Service{db: db, notifier: n, clock: c}
}

// Notifier returns the Notifier other services use.
func (s *Service) Notifier() *Notifier { return s.notifier }

// List returns actor's notifications, newest first, older than the
// notification ID before ("" for the first page), at most limit (1-100).
func (s *Service) List(ctx context.Context, actor auth.User, unreadOnly bool, before string, limit int) ([]Notification, error) {
	limit = max(1, min(limit, maxPage))
	if before == "" {
		before = "\U0010FFFF" // sorts after every ULID
	}
	unread := int64(0)
	if unreadOnly {
		unread = 1
	}
	var rows []sqlcgen.ListNotificationsRow
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		rows, err = sqlcgen.New(tx).ListNotifications(ctx, sqlcgen.ListNotificationsParams{
			UserID: actor.ID, UnreadOnly: unread, Before: before, PageSize: int64(limit)})
		return err
	})
	out := make([]Notification, len(rows))
	for i, r := range rows {
		out[i] = Notification{ID: r.ID, UserID: r.UserID, ProjectID: r.ProjectID, Kind: Kind(r.Kind),
			SubjectKind: SubjectKind(r.SubjectKind), SubjectID: r.SubjectID, CreatedAt: time.UnixMilli(r.CreatedAt).UTC(),
			Project: r.ProjectName, Review: r.ReviewNumber, Discussion: r.DiscussionID}
		if r.ReadAt.Valid {
			t := time.UnixMilli(r.ReadAt.Int64).UTC()
			out[i].ReadAt = &t
		}
	}
	return out, err
}

// MarkRead marks actor's notifications with the given IDs as read. IDs of
// other users' notifications are ignored.
func (s *Service) MarkRead(ctx context.Context, actor auth.User, ids []string) error {
	now := sql.NullInt64{Int64: s.clock.Now().UnixMilli(), Valid: true}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		for _, nid := range ids {
			if err := q.MarkNotificationRead(ctx, sqlcgen.MarkNotificationReadParams{ReadAt: now, ID: nid, UserID: actor.ID}); err != nil {
				return err
			}
		}
		return nil
	})
}

// Sweep deletes notifications older than Retention and returns how many.
func (s *Service) Sweep(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		n, err = sqlcgen.New(tx).DeleteOldNotifications(ctx, s.clock.Now().Add(-Retention).UnixMilli())
		return err
	})
	return n, err
}
