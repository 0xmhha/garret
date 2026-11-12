package review

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
	"github.com/0xmhha/garret/internal/gitrepo"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/project"
	"github.com/0xmhha/garret/internal/realtime"
	"github.com/0xmhha/garret/internal/store"
	"github.com/0xmhha/garret/internal/store/sqlcgen"
)

// Service errors.
var (
	ErrNotFound  = errors.New("review: not found")
	ErrForbidden = errors.New("review: not allowed")
	ErrInvalid   = errors.New("review: invalid input")
	ErrConflict  = errors.New("review: conflict")
)

// State is a review's state.
type State string

// Review states.
const (
	StateOpen      State = "open"
	StateCompleted State = "completed"
	StateAbandoned State = "abandoned"
)

// Verdict is a reviewer's judgement on the latest revision.
type Verdict string

// Verdicts.
const (
	VerdictApprove        Verdict = "approve"
	VerdictRequestChanges Verdict = "request_changes"
)

// MaxCommits is the commit limit per revision (data.md, Q1).
const MaxCommits = 500

const (
	maxTitleChars   = 200
	maxSubjectChars = 200
	docKind         = "review_description"
	pinFormat       = "refs/garret/reviews/%s/revisions/%d"
)

// transitions lists the allowed state changes (services.md 5.2).
var transitions = map[State][]State{
	StateOpen:      {StateCompleted, StateAbandoned},
	StateCompleted: {StateOpen},
	StateAbandoned: {StateOpen},
}

// Publisher receives events after commits.
type Publisher interface {
	Publish(realtime.Event)
}

// Review is a review without its revisions.
type Review struct {
	ID            string
	ProjectID     string
	Number        int64
	Title         string
	SourceBranch  string
	TargetBranch  string
	State         State
	AuthorID      string
	DescriptionID string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Revision is one pushed state of a review.
type Revision struct {
	ID             string
	Number         int64
	BaseSHA        string
	HeadSHA        string
	CommitsOmitted bool
	PushedBy       string
	PushedAt       time.Time
	Commits        []gitrepo.Commit
}

// UserVerdict is the latest verdict of one user.
type UserVerdict struct {
	UserID     string
	Value      Verdict
	RevisionID string
	At         time.Time
}

// Detail is a review with its revisions and latest verdicts.
type Detail struct {
	Review
	Revisions []Revision
	Verdicts  []UserVerdict
}

// RefChange is one ref updated by a push.
type RefChange struct {
	Ref string
	Old string
	New string
}

// Service manages reviews, revisions, verdicts and states. It is safe for
// concurrent use.
type Service struct {
	db       *store.DB
	audit    *audit.Recorder
	ids      *id.Generator
	clock    clock.Clock
	runner   *gitrepo.Runner
	projects *project.Service
	events   Publisher
	locks    store.KeyedMutex
}

// NewService returns a review Service.
func NewService(db *store.DB, rec *audit.Recorder, ids *id.Generator, c clock.Clock, runner *gitrepo.Runner,
	projects *project.Service, events Publisher) *Service {
	return &Service{db: db, audit: rec, ids: ids, clock: c, runner: runner, projects: projects, events: events}
}

// Open opens a review of sourceBranch against targetBranch with its first
// revision, and pins the revision's head.
func (s *Service) Open(ctx context.Context, actor auth.User, projectName, sourceBranch, targetBranch, title string) (Review, error) {
	if err := validTitle(title); err != nil {
		return Review{}, err
	}
	if gitrepo.ValidBranchRef(sourceBranch) != nil || gitrepo.ValidBranchRef(targetBranch) != nil || sourceBranch == targetBranch {
		return Review{}, fmt.Errorf("%w: source and target must be two different branches", ErrInvalid)
	}
	p, dir, err := s.project(ctx, actor, projectName)
	if err != nil {
		return Review{}, err
	}
	refs, err := s.runner.Refs(ctx, dir)
	if err != nil {
		return Review{}, err
	}
	head, target := refs[sourceBranch], refs[targetBranch]
	if head == "" || target == "" {
		return Review{}, fmt.Errorf("%w: branch", ErrNotFound)
	}
	rev, err := s.prepareRevision(ctx, dir, target, head)
	if err != nil {
		return Review{}, err
	}
	reviewID, docID, revID, err := s.newIDs()
	if err != nil {
		return Review{}, err
	}
	now := s.clock.Now().UnixMilli()
	var number int64
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		var err error
		if number, err = q.AllocateReviewNumber(ctx, p.ID); err != nil {
			return err
		}
		if err := q.InsertCollabDocument(ctx, sqlcgen.InsertCollabDocumentParams{ID: docID, ProjectID: p.ID, Kind: docKind, CreatedAt: now}); err != nil {
			return err
		}
		err = q.InsertReview(ctx, sqlcgen.InsertReviewParams{ID: reviewID, ProjectID: p.ID, Number: number, Title: title,
			SourceBranch: sourceBranch, TargetBranch: targetBranch, AuthorID: actor.ID, DescriptionDocID: docID,
			CreatedAt: now, UpdatedAt: now})
		if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: reviews.project_id, reviews.source_branch") {
			return fmt.Errorf("%w: the branch already has an open review", ErrConflict)
		}
		if err != nil {
			return err
		}
		if err := insertRevision(ctx, q, revID, reviewID, 1, rev, actor.ID, now); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ProjectID: p.ID, ActorID: actor.ID, Action: "review.created",
			SubjectKind: "review", SubjectID: reviewID, Data: map[string]any{"number": number, "source": sourceBranch, "target": targetBranch}})
	})
	if err != nil {
		return Review{}, err
	}
	if err := s.runner.UpdateRef(ctx, dir, fmt.Sprintf(pinFormat, reviewID, 1), head, ""); err != nil {
		return Review{}, err
	}
	s.events.Publish(realtime.Event{Type: "review.created", Project: p.Name, Review: int(number), ID: reviewID})
	return Review{ID: reviewID, ProjectID: p.ID, Number: number, Title: title, SourceBranch: sourceBranch,
		TargetBranch: targetBranch, State: StateOpen, AuthorID: actor.ID, DescriptionID: docID,
		CreatedAt: time.UnixMilli(now).UTC(), UpdatedAt: time.UnixMilli(now).UTC()}, nil
}

// HandlePush records a new revision for every open review whose source
// branch moved. It runs after git-receive-pack has updated the refs.
func (s *Service) HandlePush(ctx context.Context, projectName string, pusher auth.User, changes []RefChange) error {
	p, dir, err := s.project(ctx, pusher, projectName)
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range changes {
		if !isCommit(c.New) || gitrepo.ValidBranchRef(c.Ref) != nil {
			continue // deleted or unrelated refs leave reviews unchanged
		}
		var open []sqlcgen.Review
		err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
			var err error
			open, err = sqlcgen.New(tx).ListOpenReviewsOnBranch(ctx, sqlcgen.ListOpenReviewsOnBranchParams{ProjectID: p.ID, SourceBranch: c.Ref})
			return err
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, r := range open {
			errs = append(errs, s.addRevision(ctx, p, dir, r, c.New, pusher))
		}
	}
	return errors.Join(errs...)
}

func (s *Service) addRevision(ctx context.Context, p project.Project, dir string, r sqlcgen.Review, head string, pusher auth.User) error {
	unlock, err := s.locks.Lock(ctx, r.ID)
	if err != nil {
		return err
	}
	defer unlock()
	var latest sqlcgen.Revision
	err = s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		latest, err = sqlcgen.New(tx).LatestRevision(ctx, r.ID)
		return err
	})
	if err != nil {
		return err
	}
	if latest.HeadSha == head {
		return nil
	}
	target, err := s.runner.ResolveRef(ctx, dir, r.TargetBranch)
	if err != nil {
		return err
	}
	if target == "" {
		target = latest.BaseSha // the target branch was deleted; keep the old base
	}
	rev, err := s.prepareRevision(ctx, dir, target, head)
	if errors.Is(err, ErrInvalid) {
		return nil // the branch was reset to the target; nothing to review
	}
	if err != nil {
		return err
	}
	revID, err := s.ids.New()
	if err != nil {
		return err
	}
	number := latest.Number + 1
	now := s.clock.Now().UnixMilli()
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		if err := insertRevision(ctx, q, revID, r.ID, number, rev, pusher.ID, now); err != nil {
			return err
		}
		if err := q.TouchReview(ctx, sqlcgen.TouchReviewParams{UpdatedAt: now, ID: r.ID}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ProjectID: p.ID, ActorID: pusher.ID, Action: "revision.created",
			SubjectKind: "revision", SubjectID: revID, Data: map[string]any{"review": r.ID, "number": number}})
	})
	if err != nil {
		return err
	}
	if err := s.runner.UpdateRef(ctx, dir, fmt.Sprintf(pinFormat, r.ID, number), head, ""); err != nil {
		return err
	}
	s.events.Publish(realtime.Event{Type: "revision.created", Project: p.Name, Review: int(r.Number), ID: revID})
	return nil
}

// revisionData is what git reports for a new revision.
type revisionData struct {
	base, head string
	commits    []gitrepo.Commit
	omitted    bool
}

func (s *Service) prepareRevision(ctx context.Context, dir, target, head string) (revisionData, error) {
	base, err := s.runner.MergeBase(ctx, dir, target, head)
	if err != nil {
		return revisionData{}, err
	}
	commits, overflow, err := s.runner.RevList(ctx, dir, base, head, MaxCommits)
	if err != nil {
		return revisionData{}, err
	}
	if !overflow && len(commits) == 0 {
		return revisionData{}, fmt.Errorf("%w: the branch has no commits beyond its target", ErrInvalid)
	}
	return revisionData{base: base, head: head, commits: commits, omitted: overflow}, nil
}

func insertRevision(ctx context.Context, q *sqlcgen.Queries, revID, reviewID string, number int64, rev revisionData, by string, now int64) error {
	omitted := int64(0)
	if rev.omitted {
		omitted = 1
	}
	if err := q.InsertRevision(ctx, sqlcgen.InsertRevisionParams{ID: revID, ReviewID: reviewID, Number: number,
		BaseSha: rev.base, HeadSha: rev.head, CommitsOmitted: omitted, PushedBy: by, PushedAt: now}); err != nil {
		return err
	}
	for i, c := range rev.commits {
		if err := q.InsertRevisionCommit(ctx, sqlcgen.InsertRevisionCommitParams{RevisionID: revID, Position: int64(i),
			Sha: c.SHA, Subject: truncate(c.Subject), AuthorName: truncate(c.AuthorName), AuthoredAt: c.AuthoredAt * 1000}); err != nil {
			return err
		}
	}
	return nil
}

// Get returns a review with its revisions, commits and latest verdicts.
func (s *Service) Get(ctx context.Context, actor auth.User, projectName string, number int64) (Detail, error) {
	p, _, err := s.project(ctx, actor, projectName)
	if err != nil {
		return Detail{}, err
	}
	var d Detail
	err = s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		r, err := q.GetReviewByNumber(ctx, sqlcgen.GetReviewByNumberParams{ProjectID: p.ID, Number: number})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		d.Review = toReview(r)
		revs, err := q.ListRevisions(ctx, r.ID)
		if err != nil {
			return err
		}
		for _, rv := range revs {
			commits, err := q.ListRevisionCommits(ctx, rv.ID)
			if err != nil {
				return err
			}
			out := Revision{ID: rv.ID, Number: rv.Number, BaseSHA: rv.BaseSha, HeadSHA: rv.HeadSha,
				CommitsOmitted: rv.CommitsOmitted == 1, PushedBy: rv.PushedBy, PushedAt: time.UnixMilli(rv.PushedAt).UTC()}
			for _, c := range commits {
				out.Commits = append(out.Commits, gitrepo.Commit{SHA: c.Sha, Subject: c.Subject, AuthorName: c.AuthorName, AuthoredAt: c.AuthoredAt / 1000})
			}
			d.Revisions = append(d.Revisions, out)
		}
		verdicts, err := q.LatestVerdicts(ctx, r.ID)
		for _, v := range verdicts {
			d.Verdicts = append(d.Verdicts, UserVerdict{UserID: v.UserID, Value: Verdict(v.Value), RevisionID: v.RevisionID, At: time.UnixMilli(v.CreatedAt).UTC()})
		}
		return err
	})
	return d, err
}

// List returns the project's reviews, most recently active first.
func (s *Service) List(ctx context.Context, actor auth.User, projectName string) ([]Review, error) {
	p, _, err := s.project(ctx, actor, projectName)
	if err != nil {
		return nil, err
	}
	var rows []sqlcgen.Review
	err = s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		rows, err = sqlcgen.New(tx).ListReviews(ctx, p.ID)
		return err
	})
	out := make([]Review, len(rows))
	for i, r := range rows {
		out[i] = toReview(r)
	}
	return out, err
}

// RecordVerdict records actor's verdict on the latest revision of an open
// review. An author may not approve their own review.
func (s *Service) RecordVerdict(ctx context.Context, actor auth.User, projectName string, number int64, value Verdict) error {
	if value != VerdictApprove && value != VerdictRequestChanges {
		return fmt.Errorf("%w: verdict", ErrInvalid)
	}
	p, _, err := s.project(ctx, actor, projectName)
	if err != nil {
		return err
	}
	verdictID, err := s.ids.New()
	if err != nil {
		return err
	}
	var reviewID string
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		r, err := q.GetReviewByNumber(ctx, sqlcgen.GetReviewByNumberParams{ProjectID: p.ID, Number: number})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		reviewID = r.ID
		if State(r.State) != StateOpen {
			return fmt.Errorf("%w: the review is %s", ErrConflict, r.State)
		}
		if value == VerdictApprove && r.AuthorID == actor.ID {
			return fmt.Errorf("%w: authors cannot approve their own review", ErrForbidden)
		}
		latest, err := q.LatestRevision(ctx, r.ID)
		if err != nil {
			return err
		}
		now := s.clock.Now().UnixMilli()
		if err := q.InsertVerdict(ctx, sqlcgen.InsertVerdictParams{ID: verdictID, ReviewID: r.ID, RevisionID: latest.ID,
			UserID: actor.ID, Value: string(value), CreatedAt: now}); err != nil {
			return err
		}
		if err := q.TouchReview(ctx, sqlcgen.TouchReviewParams{UpdatedAt: now, ID: r.ID}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ProjectID: p.ID, ActorID: actor.ID, Action: "verdict.recorded",
			SubjectKind: "review", SubjectID: r.ID, Data: map[string]any{"value": value, "revision": latest.Number}})
	})
	if err != nil {
		return err
	}
	s.events.Publish(realtime.Event{Type: "verdict.recorded", Project: p.Name, Review: int(number), ID: reviewID})
	return nil
}

// ChangeState moves a review from from to to, if it is still in from.
func (s *Service) ChangeState(ctx context.Context, actor auth.User, projectName string, number int64, from, to State) error {
	allowed := false
	for _, t := range transitions[from] {
		allowed = allowed || t == to
	}
	if !allowed {
		return fmt.Errorf("%w: %s to %s is not a valid change", ErrInvalid, from, to)
	}
	p, _, err := s.project(ctx, actor, projectName)
	if err != nil {
		return err
	}
	changeID, err := s.ids.New()
	if err != nil {
		return err
	}
	var reviewID string
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		r, err := q.GetReviewByNumber(ctx, sqlcgen.GetReviewByNumberParams{ProjectID: p.ID, Number: number})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		reviewID = r.ID
		now := s.clock.Now().UnixMilli()
		n, err := q.ChangeReviewState(ctx, sqlcgen.ChangeReviewStateParams{State: string(to), UpdatedAt: now, ID: r.ID, State_2: string(from)})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: the review is no longer %s", ErrConflict, from)
		}
		if err := q.InsertStateChange(ctx, sqlcgen.InsertStateChangeParams{ID: changeID, ReviewID: r.ID,
			FromState: string(from), ToState: string(to), UserID: actor.ID, CreatedAt: now}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ProjectID: p.ID, ActorID: actor.ID, Action: "review.state_changed",
			SubjectKind: "review", SubjectID: r.ID, Data: map[string]any{"from": from, "to": to}})
	})
	if err != nil {
		return err
	}
	s.events.Publish(realtime.Event{Type: "review.state_changed", Project: p.Name, Review: int(number), ID: reviewID})
	return nil
}

// RepairPins re-creates missing revision refs from the stored heads, for a
// crash between a revision's commit and its pin (data.md 6.2). It runs at
// startup and returns how many refs it created.
func (s *Service) RepairPins(ctx context.Context) (int, error) {
	var pins []sqlcgen.ListRevisionPinsRow
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		pins, err = sqlcgen.New(tx).ListRevisionPins(ctx)
		return err
	})
	if err != nil {
		return 0, err
	}
	repaired := 0
	for _, p := range pins {
		dir, err := s.projects.RepoPath(p.ProjectName)
		if err != nil {
			return repaired, err
		}
		ref := fmt.Sprintf(pinFormat, p.ReviewID, p.Number)
		cur, err := s.runner.ResolveRef(ctx, dir, ref)
		if err != nil {
			return repaired, err
		}
		if cur != "" {
			continue
		}
		if err := s.runner.UpdateRef(ctx, dir, ref, p.HeadSha, ""); err != nil {
			return repaired, err
		}
		repaired++
	}
	return repaired, nil
}

func (s *Service) project(ctx context.Context, actor auth.User, name string) (project.Project, string, error) {
	p, err := s.projects.Get(ctx, actor, name)
	if errors.Is(err, project.ErrNotFound) {
		return project.Project{}, "", fmt.Errorf("%w: project", ErrNotFound)
	}
	if err != nil {
		return project.Project{}, "", err
	}
	dir, err := s.projects.RepoPath(name)
	return p, dir, err
}

func (s *Service) newIDs() (string, string, string, error) {
	var out [3]string
	for i := range out {
		v, err := s.ids.New()
		if err != nil {
			return "", "", "", err
		}
		out[i] = v
	}
	return out[0], out[1], out[2], nil
}

func validTitle(t string) error {
	n := utf8.RuneCountInString(t)
	if !utf8.ValidString(t) || strings.TrimSpace(t) == "" || n > maxTitleChars {
		return fmt.Errorf("%w: title must be 1-200 characters", ErrInvalid)
	}
	return nil
}

func truncate(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	if utf8.RuneCountInString(s) <= maxSubjectChars {
		return s
	}
	return string([]rune(s)[:maxSubjectChars])
}

func isCommit(s string) bool {
	return (len(s) == 40 || len(s) == 64) && strings.Trim(s, "0") != ""
}

func toReview(r sqlcgen.Review) Review {
	return Review{ID: r.ID, ProjectID: r.ProjectID, Number: r.Number, Title: r.Title, SourceBranch: r.SourceBranch,
		TargetBranch: r.TargetBranch, State: State(r.State), AuthorID: r.AuthorID, DescriptionID: r.DescriptionDocID,
		CreatedAt: time.UnixMilli(r.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(r.UpdatedAt).UTC()}
}
