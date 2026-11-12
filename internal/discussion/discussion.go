package discussion

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/clock"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/realtime"
	"github.com/0xmhha/garret/internal/store"
	"github.com/0xmhha/garret/internal/store/sqlcgen"
)

// Service errors.
var (
	ErrNotFound  = errors.New("discussion: not found")
	ErrForbidden = errors.New("discussion: not allowed")
	ErrInvalid   = errors.New("discussion: invalid input")
)

const (
	maxTitleChars = 200
	docKind       = "discussion_body"
)

// Discussion is a discussion post without its body text.
type Discussion struct {
	ID        string
	ProjectID string
	Title     string
	AuthorID  string
	BodyDocID string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Publisher receives events after commits.
type Publisher interface {
	Publish(realtime.Event)
}

// Service manages discussions. It is safe for concurrent use.
type Service struct {
	db     *store.DB
	audit  *audit.Recorder
	ids    *id.Generator
	clock  clock.Clock
	events Publisher
}

// New returns a discussion Service.
func New(db *store.DB, rec *audit.Recorder, ids *id.Generator, c clock.Clock, events Publisher) *Service {
	return &Service{db: db, audit: rec, ids: ids, clock: c, events: events}
}

// Create creates a discussion with an empty collaborative body.
func (s *Service) Create(ctx context.Context, actor auth.User, projectName, title string) (Discussion, error) {
	if err := validTitle(title); err != nil {
		return Discussion{}, err
	}
	discussionID, err := s.ids.New()
	if err != nil {
		return Discussion{}, err
	}
	docID, err := s.ids.New()
	if err != nil {
		return Discussion{}, err
	}
	now := s.clock.Now().UnixMilli()
	var projectID string
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		p, err := q.GetProjectByName(ctx, projectName)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: project", ErrNotFound)
		}
		if err != nil {
			return err
		}
		projectID = p.ID
		if err := q.InsertCollabDocument(ctx, sqlcgen.InsertCollabDocumentParams{ID: docID, ProjectID: p.ID, Kind: docKind, CreatedAt: now}); err != nil {
			return err
		}
		if err := q.InsertDiscussion(ctx, sqlcgen.InsertDiscussionParams{ID: discussionID, ProjectID: p.ID, Title: title,
			AuthorID: actor.ID, BodyDocID: docID, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ProjectID: p.ID, ActorID: actor.ID, Action: "discussion.created",
			SubjectKind: "discussion", SubjectID: discussionID})
	})
	if err != nil {
		return Discussion{}, err
	}
	s.events.Publish(realtime.Event{Type: "discussion.created", Project: projectName, ID: discussionID})
	t := time.UnixMilli(now).UTC()
	return Discussion{ID: discussionID, ProjectID: projectID, Title: title, AuthorID: actor.ID, BodyDocID: docID, CreatedAt: t, UpdatedAt: t}, nil
}

// Get returns a discussion that is not deleted.
func (s *Service) Get(ctx context.Context, _ auth.User, projectName, discussionID string) (Discussion, error) {
	var out Discussion
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		d, err := lookup(ctx, sqlcgen.New(tx), projectName, discussionID)
		out = toDiscussion(d)
		return err
	})
	return out, err
}

// List returns the project's discussions, most recently active first.
func (s *Service) List(ctx context.Context, _ auth.User, projectName string) ([]Discussion, error) {
	var out []Discussion
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		p, err := q.GetProjectByName(ctx, projectName)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: project", ErrNotFound)
		}
		if err != nil {
			return err
		}
		rows, err := q.ListDiscussions(ctx, p.ID)
		for _, d := range rows {
			out = append(out, toDiscussion(d))
		}
		return err
	})
	return out, err
}

// Rename changes the title. The author or an administrator may rename.
func (s *Service) Rename(ctx context.Context, actor auth.User, projectName, discussionID, title string) error {
	if err := validTitle(title); err != nil {
		return err
	}
	return s.change(ctx, actor, projectName, discussionID, "discussion.renamed", func(q *sqlcgen.Queries, now int64) error {
		_, err := q.RenameDiscussion(ctx, sqlcgen.RenameDiscussionParams{Title: title, UpdatedAt: now, ID: discussionID})
		return err
	})
}

// Delete soft-deletes a discussion. The author or an administrator may
// delete.
func (s *Service) Delete(ctx context.Context, actor auth.User, projectName, discussionID string) error {
	return s.change(ctx, actor, projectName, discussionID, "discussion.deleted", func(q *sqlcgen.Queries, now int64) error {
		_, err := q.SoftDeleteDiscussion(ctx, sqlcgen.SoftDeleteDiscussionParams{DeletedAt: sql.NullInt64{Int64: now, Valid: true}, ID: discussionID})
		return err
	})
}

func (s *Service) change(ctx context.Context, actor auth.User, projectName, discussionID, action string, apply func(*sqlcgen.Queries, int64) error) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		d, err := lookup(ctx, q, projectName, discussionID)
		if err != nil {
			return err
		}
		if d.AuthorID != actor.ID && !actor.IsAdmin() {
			return ErrForbidden
		}
		if err := apply(q, s.clock.Now().UnixMilli()); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ProjectID: d.ProjectID, ActorID: actor.ID, Action: action,
			SubjectKind: "discussion", SubjectID: discussionID})
	})
}

func lookup(ctx context.Context, q *sqlcgen.Queries, projectName, discussionID string) (sqlcgen.Discussion, error) {
	p, err := q.GetProjectByName(ctx, projectName)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlcgen.Discussion{}, fmt.Errorf("%w: project", ErrNotFound)
	}
	if err != nil {
		return sqlcgen.Discussion{}, err
	}
	d, err := q.GetDiscussion(ctx, sqlcgen.GetDiscussionParams{ID: discussionID, ProjectID: p.ID})
	if errors.Is(err, sql.ErrNoRows) || err == nil && d.DeletedAt.Valid {
		return sqlcgen.Discussion{}, ErrNotFound
	}
	return d, err
}

func validTitle(t string) error {
	if !utf8.ValidString(t) || strings.TrimSpace(t) == "" || utf8.RuneCountInString(t) > maxTitleChars {
		return fmt.Errorf("%w: title must be 1-200 characters", ErrInvalid)
	}
	return nil
}

func toDiscussion(d sqlcgen.Discussion) Discussion {
	return Discussion{ID: d.ID, ProjectID: d.ProjectID, Title: d.Title, AuthorID: d.AuthorID, BodyDocID: d.BodyDocID,
		CreatedAt: time.UnixMilli(d.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(d.UpdatedAt).UTC()}
}
