package review

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/gitrepo"
	"github.com/0xmhha/garret/internal/store/sqlcgen"
)

// MaxHistoryPage is the largest page History returns.
const MaxHistoryPage = 100

// HistoryEntry is one event of a review's audit trail.
type HistoryEntry struct {
	ID          string
	ActorID     string // "" for an event without an actor
	Action      string
	SubjectKind string
	SubjectID   string
	Data        map[string]any
	At          time.Time
}

// File returns the content of path at commit, which must belong to the
// review (services.md 5.3). Binary content is ErrInvalid and content over
// the diff limit is gitrepo.ErrTooLarge.
func (s *Service) File(ctx context.Context, actor auth.User, projectName string, number int64, commit, path string) ([]byte, error) {
	if !validRepoPath(path) {
		return nil, fmt.Errorf("%w: path", ErrInvalid)
	}
	_, dir, revs, err := s.revisions(ctx, actor, projectName, number)
	if err != nil {
		return nil, err
	}
	if !knownCommits(revs)[commit] {
		return nil, fmt.Errorf("%w: the commit is not part of this review", ErrInvalid)
	}
	content, err := s.runner.FileAt(ctx, dir, commit, path, int64(DiffLimits.MaxBytes))
	if errors.Is(err, gitrepo.ErrNotFound) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(content, 0) >= 0 || !utf8.Valid(content) {
		return nil, fmt.Errorf("%w: binary file", ErrInvalid)
	}
	return content, nil
}

// validRepoPath accepts a path inside a tree: not empty, relative, without
// NUL and without "." or ".." components.
func validRepoPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) {
		return false
	}
	for part := range strings.SplitSeq(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// History returns up to limit events of the review's audit trail after the
// event ID after, oldest first, and the cursor of the next page ("" at the
// end).
func (s *Service) History(ctx context.Context, actor auth.User, projectName string, number int64, after string, limit int) ([]HistoryEntry, string, error) {
	if limit < 1 || limit > MaxHistoryPage {
		return nil, "", fmt.Errorf("%w: limit must be 1 to %d", ErrInvalid, MaxHistoryPage)
	}
	r, _, _, err := s.revisions(ctx, actor, projectName, number)
	if err != nil {
		return nil, "", err
	}
	var rows []sqlcgen.AuditEvent
	err = s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		// One extra row tells whether another page follows.
		rows, err = sqlcgen.New(tx).ListReviewHistory(ctx, sqlcgen.ListReviewHistoryParams{After: after, ReviewID: r.ID, MaxRows: int64(limit) + 1})
		return err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = rows[limit-1].ID
	}
	out := make([]HistoryEntry, len(rows))
	for i, e := range rows {
		data := map[string]any{}
		if err := json.Unmarshal([]byte(e.Data), &data); err != nil {
			return nil, "", fmt.Errorf("review: audit event %s: %w", e.ID, err)
		}
		out[i] = HistoryEntry{ID: e.ID, ActorID: e.ActorID.String, Action: e.Action, SubjectKind: e.SubjectKind,
			SubjectID: e.SubjectID, Data: data, At: time.UnixMilli(e.CreatedAt).UTC()}
	}
	return out, next, nil
}
