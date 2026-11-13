package comment

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/clock"
	"github.com/0xmhha/garret/internal/gitrepo"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/inbox"
	"github.com/0xmhha/garret/internal/project"
	"github.com/0xmhha/garret/internal/realtime"
	"github.com/0xmhha/garret/internal/store"
	"github.com/0xmhha/garret/internal/store/sqlcgen"
)

// Service errors.
var (
	ErrNotFound  = errors.New("comment: not found")
	ErrForbidden = errors.New("comment: not allowed")
	ErrInvalid   = errors.New("comment: invalid input")
	ErrConflict  = errors.New("comment: conflict")
)

// View is the diff view a code comment was written on.
type View string

// Views (architecture.md, Reviews and revisions).
const (
	ViewRevision View = "revision"
	ViewCommit   View = "commit"
	ViewRange    View = "range"
)

// Side is the side of the diff a code comment is on.
type Side string

// Sides.
const (
	SideOld Side = "old"
	SideNew Side = "new"
)

const (
	maxBodyBytes   = 65536
	maxAnchorPath  = 4096
	maxAnchorFile  = 2 << 20 // the diff limit of atomic.md Q5
	mentionPattern = `(?:^|[^A-Za-z0-9_@-])@([a-z0-9][a-z0-9_-]{1,31})\b`
)

var mentionRE = regexp.MustCompile(mentionPattern)

// Anchor places a code comment. Start and End are 0 for a file-level
// comment.
type Anchor struct {
	Revision   int64
	View       View
	From, To   string
	Path       string
	Side       Side
	Start, End int
}

// NewComment is the input of Create. Exactly one of Review and Discussion is
// set. ReplyTo is the thread root for a reply.
type NewComment struct {
	Review     int64
	Discussion string
	ReplyTo    string
	Anchor     *Anchor
	Body       string
	// Attachments are IDs of the project's attachments to link.
	Attachments []string
}

// Comment is a comment with its current body. Body is empty for a deleted
// comment.
type Comment struct {
	ID           string
	ProjectID    string
	ReviewID     string
	DiscussionID string
	ThreadRootID string
	AuthorID     string
	RevisionID   string
	Anchor       *Anchor
	Resolved     bool
	Version      int64
	Body         string
	CreatedAt    time.Time
	Deleted      bool
	Attachments  []string
}

// Version is one stored version of a comment body.
type Version struct {
	Number    int64
	Body      string
	CreatedAt time.Time
}

// Publisher receives events after commits.
type Publisher interface {
	Publish(realtime.Event)
}

// Service manages comments. It is safe for concurrent use.
type Service struct {
	db       *store.DB
	audit    *audit.Recorder
	ids      *id.Generator
	clock    clock.Clock
	runner   *gitrepo.Runner
	projects *project.Service
	events   Publisher
	notifier *inbox.Notifier
}

// New returns a comment Service.
func New(db *store.DB, rec *audit.Recorder, ids *id.Generator, c clock.Clock, runner *gitrepo.Runner,
	projects *project.Service, events Publisher, notifier *inbox.Notifier) *Service {
	return &Service{db: db, audit: rec, ids: ids, clock: c, runner: runner, projects: projects, events: events, notifier: notifier}
}

// parent is a resolved comment target.
type parent struct {
	project      project.Project
	dir          string
	reviewID     string
	reviewNumber int64
	reviewAuthor string
	discussionID string
}

// Create adds a comment, notifies mentioned users, the root's author for a
// reply and the review's author, and publishes comment.created.
func (s *Service) Create(ctx context.Context, actor auth.User, projectName string, in NewComment) (Comment, error) {
	if err := validBody(in.Body); err != nil {
		return Comment{}, err
	}
	if (in.Review == 0) == (in.Discussion == "") {
		return Comment{}, fmt.Errorf("%w: exactly one of review and discussion", ErrInvalid)
	}
	if in.Anchor != nil && (in.Review == 0 || in.ReplyTo != "") {
		return Comment{}, fmt.Errorf("%w: only a root comment on a review can be anchored", ErrInvalid)
	}
	par, err := s.resolveParent(ctx, actor, projectName, in.Review, in.Discussion)
	if err != nil {
		return Comment{}, err
	}
	var revisionID string
	if in.Anchor != nil {
		if revisionID, err = s.validateAnchor(ctx, par, in.Anchor); err != nil {
			return Comment{}, err
		}
	}
	commentID, err := s.ids.New()
	if err != nil {
		return Comment{}, err
	}
	now := s.clock.Now().UnixMilli()
	var notified []string
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		var rootAuthor string
		if in.ReplyTo != "" {
			root, err := q.GetComment(ctx, in.ReplyTo)
			if errors.Is(err, sql.ErrNoRows) || err == nil && !sameParent(root, par) {
				return fmt.Errorf("%w: thread", ErrNotFound)
			}
			if err != nil {
				return err
			}
			if root.ThreadRootID.Valid {
				return fmt.Errorf("%w: replies go to the thread's root comment", ErrInvalid)
			}
			if root.DeletedAt.Valid {
				return fmt.Errorf("%w: the thread was deleted", ErrConflict)
			}
			rootAuthor = root.AuthorID
		}
		if err := q.InsertComment(ctx, insertParams(commentID, par, in, actor.ID, revisionID, now)); err != nil {
			return err
		}
		if err := q.InsertCommentVersion(ctx, sqlcgen.InsertCommentVersionParams{CommentID: commentID, Version: 1, Body: in.Body, CreatedAt: now}); err != nil {
			return err
		}
		for _, aid := range in.Attachments {
			a, err := q.GetAttachment(ctx, aid)
			if errors.Is(err, sql.ErrNoRows) || err == nil && (a.ProjectID != par.project.ID || a.DeletedAt.Valid) {
				return fmt.Errorf("%w: attachment %s", ErrInvalid, aid)
			}
			if err != nil {
				return err
			}
			if err := q.InsertCommentAttachment(ctx, sqlcgen.InsertCommentAttachmentParams{CommentID: commentID, AttachmentID: aid}); err != nil {
				return err
			}
		}
		recipients, err := s.recipients(ctx, q, actor.ID, in.Body, "", rootAuthor, par.reviewAuthor)
		if err != nil {
			return err
		}
		for _, r := range recipients {
			if err := s.notifier.Notify(ctx, tx, inbox.Notification{UserID: r.user, ProjectID: par.project.ID, Kind: r.kind,
				SubjectKind: inbox.SubjectComment, SubjectID: commentID}); err != nil {
				return err
			}
			notified = append(notified, r.user)
		}
		if err := touchParent(ctx, q, par, now); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ProjectID: par.project.ID, ActorID: actor.ID, Action: "comment.created",
			SubjectKind: "comment", SubjectID: commentID, Data: map[string]any{"review": par.reviewID, "discussion": par.discussionID}})
	})
	if err != nil {
		return Comment{}, err
	}
	s.publish(par, "comment.created", commentID, notified)
	return Comment{ID: commentID, ProjectID: par.project.ID, ReviewID: par.reviewID, DiscussionID: par.discussionID,
		ThreadRootID: in.ReplyTo, AuthorID: actor.ID, RevisionID: revisionID, Anchor: in.Anchor, Version: 1, Body: in.Body,
		CreatedAt: time.UnixMilli(now).UTC(), Attachments: in.Attachments}, nil
}

// Edit replaces the body of actor's comment, keeping the earlier version.
// expectedVersion must be the current version, so concurrent edits fail
// with ErrConflict instead of overwriting each other.
func (s *Service) Edit(ctx context.Context, actor auth.User, commentID string, expectedVersion int64, body string) (Comment, error) {
	if err := validBody(body); err != nil {
		return Comment{}, err
	}
	var out Comment
	var par parent
	var notified []string
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		c, err := q.GetComment(ctx, commentID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if c.AuthorID != actor.ID {
			return ErrForbidden
		}
		n, err := q.BumpCommentVersion(ctx, sqlcgen.BumpCommentVersionParams{ID: commentID, AuthorID: actor.ID, CurrentVersion: expectedVersion})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: the comment changed or was deleted", ErrConflict)
		}
		previous, err := q.GetCommentBody(ctx, sqlcgen.GetCommentBodyParams{CommentID: commentID, Version: expectedVersion})
		if err != nil {
			return err
		}
		now := s.clock.Now().UnixMilli()
		if err := q.InsertCommentVersion(ctx, sqlcgen.InsertCommentVersionParams{CommentID: commentID, Version: expectedVersion + 1, Body: body, CreatedAt: now}); err != nil {
			return err
		}
		recipients, err := s.recipients(ctx, q, actor.ID, body, previous, "", "")
		if err != nil {
			return err
		}
		for _, r := range recipients {
			if err := s.notifier.Notify(ctx, tx, inbox.Notification{UserID: r.user, ProjectID: c.ProjectID, Kind: r.kind,
				SubjectKind: inbox.SubjectComment, SubjectID: commentID}); err != nil {
				return err
			}
			notified = append(notified, r.user)
		}
		out = toComment(c, body)
		out.Version = expectedVersion + 1
		par, err = s.parentOf(ctx, q, c)
		if err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ProjectID: c.ProjectID, ActorID: actor.ID, Action: "comment.edited",
			SubjectKind: "comment", SubjectID: commentID, Data: map[string]any{"version": expectedVersion + 1}})
	})
	if err != nil {
		return Comment{}, err
	}
	s.publish(par, "comment.updated", commentID, notified)
	return out, nil
}

// Resolve marks a thread resolved or unresolved. Any member may resolve.
func (s *Service) Resolve(ctx context.Context, actor auth.User, rootID string, resolved bool) error {
	var par parent
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		c, err := q.GetComment(ctx, rootID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if c.ThreadRootID.Valid {
			return fmt.Errorf("%w: only a thread's root comment can be resolved", ErrInvalid)
		}
		value := int64(0)
		if resolved {
			value = 1
		}
		if _, err := q.SetCommentResolved(ctx, sqlcgen.SetCommentResolvedParams{Resolved: value, ID: rootID}); err != nil {
			return err
		}
		if par, err = s.parentOf(ctx, q, c); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ProjectID: c.ProjectID, ActorID: actor.ID, Action: "comment.resolved",
			SubjectKind: "comment", SubjectID: rootID, Data: map[string]any{"resolved": resolved}})
	})
	if err != nil {
		return err
	}
	s.publish(par, "comment.resolved", rootID, nil)
	return nil
}

// Delete soft-deletes a comment. Its author or an administrator may delete.
func (s *Service) Delete(ctx context.Context, actor auth.User, commentID string) error {
	var par parent
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		c, err := q.GetComment(ctx, commentID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if c.AuthorID != actor.ID && !actor.IsAdmin() {
			return ErrForbidden
		}
		if _, err := q.SoftDeleteComment(ctx, sqlcgen.SoftDeleteCommentParams{
			DeletedAt: sql.NullInt64{Int64: s.clock.Now().UnixMilli(), Valid: true},
			DeletedBy: sql.NullString{String: actor.ID, Valid: true}, ID: commentID}); err != nil {
			return err
		}
		if par, err = s.parentOf(ctx, q, c); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ProjectID: c.ProjectID, ActorID: actor.ID, Action: "comment.deleted",
			SubjectKind: "comment", SubjectID: commentID})
	})
	if err != nil {
		return err
	}
	s.publish(par, "comment.updated", commentID, nil)
	return nil
}

// Versions returns every version of a comment, oldest first. A deleted
// comment's versions stay visible; only erasure removes them.
func (s *Service) Versions(ctx context.Context, _ auth.User, commentID string) ([]Version, error) {
	var rows []sqlcgen.CommentVersion
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		rows, err = sqlcgen.New(tx).ListCommentVersions(ctx, commentID)
		return err
	})
	out := make([]Version, len(rows))
	for i, v := range rows {
		out[i] = Version{Number: v.Version, Body: v.Body, CreatedAt: time.UnixMilli(v.CreatedAt).UTC()}
	}
	return out, err
}

// ListForReview returns every comment of a review in creation order.
func (s *Service) ListForReview(ctx context.Context, actor auth.User, projectName string, number int64) ([]Comment, error) {
	par, err := s.resolveParent(ctx, actor, projectName, number, "")
	if err != nil {
		return nil, err
	}
	var out []Comment
	err = s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		rows, err := q.ListReviewComments(ctx, sql.NullString{String: par.reviewID, Valid: true})
		if err != nil {
			return err
		}
		// Anchors store the revision's ID; callers read its number.
		revs, err := q.ListRevisions(ctx, par.reviewID)
		if err != nil {
			return err
		}
		numbers := make(map[string]int64, len(revs))
		for _, rv := range revs {
			numbers[rv.ID] = rv.Number
		}
		for _, r := range rows {
			c := toComment(commentFromReviewRow(r), r.Body)
			if c.Anchor != nil {
				c.Anchor.Revision = numbers[c.RevisionID]
			}
			if c.Attachments, err = attachmentIDs(ctx, q, c); err != nil {
				return err
			}
			out = append(out, c)
		}
		return nil
	})
	return out, err
}

// ListForDiscussion returns every comment of a discussion in creation order.
func (s *Service) ListForDiscussion(ctx context.Context, actor auth.User, projectName, discussionID string) ([]Comment, error) {
	par, err := s.resolveParent(ctx, actor, projectName, 0, discussionID)
	if err != nil {
		return nil, err
	}
	var out []Comment
	err = s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		rows, err := q.ListDiscussionComments(ctx, sql.NullString{String: par.discussionID, Valid: true})
		if err != nil {
			return err
		}
		for _, r := range rows {
			c := toComment(commentFromDiscussionRow(r), r.Body)
			if c.Attachments, err = attachmentIDs(ctx, q, c); err != nil {
				return err
			}
			out = append(out, c)
		}
		return nil
	})
	return out, err
}

func (s *Service) resolveParent(ctx context.Context, actor auth.User, projectName string, reviewNumber int64, discussionID string) (parent, error) {
	p, err := s.projects.Get(ctx, actor, projectName)
	if errors.Is(err, project.ErrNotFound) {
		return parent{}, fmt.Errorf("%w: project", ErrNotFound)
	}
	if err != nil {
		return parent{}, err
	}
	dir, err := s.projects.RepoPath(projectName)
	if err != nil {
		return parent{}, err
	}
	par := parent{project: p, dir: dir}
	err = s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		if reviewNumber != 0 {
			r, err := q.GetReviewByNumber(ctx, sqlcgen.GetReviewByNumberParams{ProjectID: p.ID, Number: reviewNumber})
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: review", ErrNotFound)
			}
			par.reviewID, par.reviewNumber, par.reviewAuthor = r.ID, r.Number, r.AuthorID
			return err
		}
		d, err := q.GetDiscussion(ctx, sqlcgen.GetDiscussionParams{ID: discussionID, ProjectID: p.ID})
		if errors.Is(err, sql.ErrNoRows) || err == nil && d.DeletedAt.Valid {
			return fmt.Errorf("%w: discussion", ErrNotFound)
		}
		par.discussionID = d.ID
		return err
	})
	return par, err
}

func (s *Service) parentOf(ctx context.Context, q *sqlcgen.Queries, c sqlcgen.Comment) (parent, error) {
	p, err := q.GetProjectByID(ctx, c.ProjectID)
	if err != nil {
		return parent{}, err
	}
	par := parent{project: project.Project{ID: p.ID, Name: p.Name}, reviewID: c.ReviewID.String, discussionID: c.DiscussionID.String}
	if c.ReviewID.Valid {
		r, err := q.GetReviewByNumberOrID(ctx, c.ReviewID.String)
		if err != nil {
			return parent{}, err
		}
		par.reviewNumber = r.Number
	}
	return par, nil
}

// validateAnchor checks that the anchor's commits belong to the revision in
// the way its view requires, and that its lines exist in the file on its
// side. It returns the revision ID.
func (s *Service) validateAnchor(ctx context.Context, par parent, a *Anchor) (string, error) {
	if a.Side != SideOld && a.Side != SideNew {
		return "", fmt.Errorf("%w: side", ErrInvalid)
	}
	if a.Path == "" || len(a.Path) > maxAnchorPath {
		return "", fmt.Errorf("%w: path", ErrInvalid)
	}
	if (a.Start == 0) != (a.End == 0) || a.Start < 0 || a.End < a.Start {
		return "", fmt.Errorf("%w: lines", ErrInvalid)
	}
	var rev sqlcgen.Revision
	var commits []sqlcgen.RevisionCommit
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		var err error
		rev, err = q.GetRevisionByNumber(ctx, sqlcgen.GetRevisionByNumberParams{ReviewID: par.reviewID, Number: a.Revision})
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: revision", ErrInvalid)
		}
		if err != nil {
			return err
		}
		commits, err = q.ListRevisionCommits(ctx, rev.ID)
		return err
	})
	if err != nil {
		return "", err
	}
	if !viewMatches(a, rev, commits) {
		return "", fmt.Errorf("%w: the commits do not match the revision and view", ErrInvalid)
	}
	sideCommit := a.To
	if a.Side == SideOld {
		sideCommit = a.From
	}
	content, err := s.runner.FileAt(ctx, par.dir, sideCommit, a.Path, maxAnchorFile)
	if err != nil {
		return "", fmt.Errorf("%w: the file does not exist on that side", ErrInvalid)
	}
	if a.End > lineCount(content) {
		return "", fmt.Errorf("%w: the lines are outside the file", ErrInvalid)
	}
	return rev.ID, nil
}

func viewMatches(a *Anchor, rev sqlcgen.Revision, commits []sqlcgen.RevisionCommit) bool {
	order := []string{rev.BaseSha}
	for _, c := range commits {
		order = append(order, c.Sha)
	}
	from, to := slices.Index(order, a.From), slices.Index(order, a.To)
	switch a.View {
	case ViewRevision:
		return a.From == rev.BaseSha && a.To == rev.HeadSha
	case ViewCommit:
		return to > 0 && from == to-1
	case ViewRange:
		return from >= 0 && to > from
	}
	return false
}

func lineCount(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := bytes.Count(b, []byte{'\n'})
	if b[len(b)-1] != '\n' {
		n++
	}
	return n
}

type recipient struct {
	user string
	kind inbox.Kind
}

// recipients lists who to notify, at most once each and never the actor:
// users newly mentioned in body (not already in previous), then the
// thread root's author, then the review's author.
func (s *Service) recipients(ctx context.Context, q *sqlcgen.Queries, actor, body, previous, rootAuthor, reviewAuthor string) ([]recipient, error) {
	names := mentions(body)
	old := mentions(previous)
	names = slices.DeleteFunc(names, func(n string) bool { return slices.Contains(old, n) })
	seen := map[string]bool{actor: true}
	var out []recipient
	if len(names) > 0 {
		users, err := q.ListEnabledUsersByName(ctx, names)
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			if !seen[u.ID] {
				seen[u.ID] = true
				out = append(out, recipient{u.ID, inbox.KindMention})
			}
		}
	}
	for _, r := range []recipient{{rootAuthor, inbox.KindReplyToMe}, {reviewAuthor, inbox.KindCommentOnMyReview}} {
		if r.user != "" && !seen[r.user] {
			seen[r.user] = true
			out = append(out, r)
		}
	}
	return out, nil
}

func mentions(body string) []string {
	var out []string
	for _, m := range mentionRE.FindAllStringSubmatch(body, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

func (s *Service) publish(par parent, kind, commentID string, notified []string) {
	s.events.Publish(realtime.Event{Type: kind, Project: par.project.Name, Review: int(par.reviewNumber), ID: commentID})
	for _, u := range notified {
		s.events.Publish(realtime.Event{Type: "notification.created", Project: par.project.Name, User: u})
	}
}

func touchParent(ctx context.Context, q *sqlcgen.Queries, par parent, now int64) error {
	if par.reviewID != "" {
		return q.TouchReview(ctx, sqlcgen.TouchReviewParams{UpdatedAt: now, ID: par.reviewID})
	}
	return q.TouchDiscussion(ctx, sqlcgen.TouchDiscussionParams{UpdatedAt: now, ID: par.discussionID})
}

func sameParent(c sqlcgen.Comment, par parent) bool {
	return c.ProjectID == par.project.ID && c.ReviewID.String == par.reviewID && c.DiscussionID.String == par.discussionID
}

func validBody(body string) error {
	if len(body) == 0 || len(body) > maxBodyBytes || !utf8.ValidString(body) {
		return fmt.Errorf("%w: body must be 1-65536 bytes of UTF-8", ErrInvalid)
	}
	return nil
}

func insertParams(commentID string, par parent, in NewComment, author, revisionID string, now int64) sqlcgen.InsertCommentParams {
	p := sqlcgen.InsertCommentParams{
		ID: commentID, ProjectID: par.project.ID,
		ReviewID:     sql.NullString{String: par.reviewID, Valid: par.reviewID != ""},
		DiscussionID: sql.NullString{String: par.discussionID, Valid: par.discussionID != ""},
		ThreadRootID: sql.NullString{String: in.ReplyTo, Valid: in.ReplyTo != ""},
		AuthorID:     author, CreatedAt: now,
	}
	if a := in.Anchor; a != nil {
		p.AnchorRevisionID = sql.NullString{String: revisionID, Valid: true}
		p.AnchorView = sql.NullString{String: string(a.View), Valid: true}
		p.AnchorFromSha = sql.NullString{String: a.From, Valid: true}
		p.AnchorToSha = sql.NullString{String: a.To, Valid: true}
		p.AnchorPath = sql.NullString{String: a.Path, Valid: true}
		p.AnchorSide = sql.NullString{String: string(a.Side), Valid: true}
		p.AnchorStartLine = sql.NullInt64{Int64: int64(a.Start), Valid: a.Start > 0}
		p.AnchorEndLine = sql.NullInt64{Int64: int64(a.End), Valid: a.End > 0}
	}
	return p
}

func toComment(c sqlcgen.Comment, body string) Comment {
	out := Comment{ID: c.ID, ProjectID: c.ProjectID, ReviewID: c.ReviewID.String, DiscussionID: c.DiscussionID.String,
		ThreadRootID: c.ThreadRootID.String, AuthorID: c.AuthorID, RevisionID: c.AnchorRevisionID.String,
		Resolved: c.Resolved == 1, Version: c.CurrentVersion, Body: body, CreatedAt: time.UnixMilli(c.CreatedAt).UTC(),
		Deleted: c.DeletedAt.Valid}
	if c.AnchorPath.Valid {
		out.Anchor = &Anchor{View: View(c.AnchorView.String), From: c.AnchorFromSha.String, To: c.AnchorToSha.String,
			Path: c.AnchorPath.String, Side: Side(c.AnchorSide.String),
			Start: int(c.AnchorStartLine.Int64), End: int(c.AnchorEndLine.Int64)}
	}
	if out.Deleted {
		out.Body = ""
	}
	return out
}

func commentFromReviewRow(r sqlcgen.ListReviewCommentsRow) sqlcgen.Comment {
	return sqlcgen.Comment{ID: r.ID, ProjectID: r.ProjectID, ReviewID: r.ReviewID, DiscussionID: r.DiscussionID,
		ThreadRootID: r.ThreadRootID, AuthorID: r.AuthorID, AnchorRevisionID: r.AnchorRevisionID, AnchorView: r.AnchorView,
		AnchorFromSha: r.AnchorFromSha, AnchorToSha: r.AnchorToSha, AnchorPath: r.AnchorPath, AnchorSide: r.AnchorSide,
		AnchorStartLine: r.AnchorStartLine, AnchorEndLine: r.AnchorEndLine, Resolved: r.Resolved,
		CurrentVersion: r.CurrentVersion, CreatedAt: r.CreatedAt, DeletedAt: r.DeletedAt, DeletedBy: r.DeletedBy}
}

func commentFromDiscussionRow(r sqlcgen.ListDiscussionCommentsRow) sqlcgen.Comment {
	return commentFromReviewRow(sqlcgen.ListReviewCommentsRow(r))
}

// attachmentIDs lists a comment's linked attachments that are not deleted.
// A deleted comment shows none.
func attachmentIDs(ctx context.Context, q *sqlcgen.Queries, c Comment) ([]string, error) {
	if c.Deleted {
		return nil, nil
	}
	rows, err := q.ListCommentAttachments(ctx, c.ID)
	var out []string
	for _, a := range rows {
		if !a.DeletedAt.Valid {
			out = append(out, a.ID)
		}
	}
	return out, err
}
