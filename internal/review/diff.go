package review

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/gitrepo"
	"github.com/0xmhha/garret/internal/store/sqlcgen"
)

// DiffLimits are the per-file limits of atomic.md Q5.
var DiffLimits = gitrepo.Limits{MaxBytes: 2 << 20, MaxLines: 20000}

// DiffQuery selects one of the four views of a review. Exactly one form is
// used: Revision; Commit; FromRevision and ToRevision; or From and To
// commits of the same revision.
type DiffQuery struct {
	Revision     int64
	Commit       string
	FromRevision int64
	ToRevision   int64
	From, To     string
}

// DiffSummary is the list of files a view changes.
type DiffSummary struct {
	From, To string
	Files    []gitrepo.FileDiff
	// BasesDiffer is set for a revision pair whose bases differ (the branch
	// was rebased), so the diff also contains upstream changes.
	BasesDiffer bool
}

// Diff resolves a view to two commits and lists the changed files.
func (s *Service) Diff(ctx context.Context, actor auth.User, projectName string, number int64, q DiffQuery) (DiffSummary, error) {
	_, dir, revs, err := s.revisions(ctx, actor, projectName, number)
	if err != nil {
		return DiffSummary{}, err
	}
	from, to, differ, err := resolveView(revs, q)
	if err != nil {
		return DiffSummary{}, err
	}
	files, err := s.runner.DiffSummary(ctx, dir, from, to)
	if err != nil {
		return DiffSummary{}, err
	}
	return DiffSummary{From: from, To: to, Files: files, BasesDiffer: differ}, nil
}

// Patch is a view's commits as one patch file (decisions.md D16).
type Patch struct {
	Name string // a download name such as libfoo-12-r2.patch
	Data []byte
}

// shortSHA is how many characters of a commit a patch name carries.
const shortSHA = 7

// Patch returns the commits of a revision, a commit or a range as git
// format-patch output for git am. A comparison of two revisions has no
// patch, since the revisions may have different bases.
func (s *Service) Patch(ctx context.Context, actor auth.User, projectName string, number int64, q DiffQuery) (Patch, error) {
	if q.FromRevision != 0 || q.ToRevision != 0 {
		return Patch{}, fmt.Errorf("%w: a comparison of revisions has no patch", ErrInvalid)
	}
	_, dir, revs, err := s.revisions(ctx, actor, projectName, number)
	if err != nil {
		return Patch{}, err
	}
	from, to, _, err := resolveView(revs, q)
	if err != nil {
		return Patch{}, err
	}
	name := fmt.Sprintf("%s-%d-r%d", projectName, number, patchRevision(revs, q, to))
	switch {
	case q.Commit != "":
		name += "-" + to[:shortSHA]
	case q.From != "":
		name += "-" + from[:shortSHA] + "-" + to[:shortSHA]
	}
	data, err := s.runner.FormatPatch(ctx, dir, from, to)
	if err != nil {
		return Patch{}, err
	}
	return Patch{Name: name + ".patch", Data: data}, nil
}

// patchRevision is the revision a patch comes from: the one asked for, or
// the latest revision holding the patch's last commit.
func patchRevision(revs []revisionInfo, q DiffQuery, to string) int64 {
	if q.Revision != 0 {
		return q.Revision
	}
	var n int64
	for _, r := range revs {
		if slices.Contains(r.commits, to) {
			n = r.row.Number
		}
	}
	return n
}

// FileDiff returns the hunks of one file between two commits of the review.
// Pass both paths for a rename and "" for a side that does not exist. Files
// over the limits return gitrepo.ErrTooLarge.
func (s *Service) FileDiff(ctx context.Context, actor auth.User, projectName string, number int64, from, to, oldPath, newPath string) ([]gitrepo.Hunk, error) {
	_, dir, revs, err := s.revisions(ctx, actor, projectName, number)
	if err != nil {
		return nil, err
	}
	known := knownCommits(revs)
	if !known[from] || !known[to] {
		return nil, fmt.Errorf("%w: the commits are not part of this review", ErrInvalid)
	}
	return s.fileHunks(ctx, dir, from, to, oldPath, newPath)
}

func (s *Service) fileHunks(ctx context.Context, dir, from, to, oldPath, newPath string) ([]gitrepo.Hunk, error) {
	k := cacheKey{from, to, oldPath, newPath}
	if h, ok := s.cache.get(k); ok {
		return h, nil
	}
	h, err := s.runner.DiffFile(ctx, dir, from, to, oldPath, newPath, DiffLimits)
	if err != nil {
		return nil, err
	}
	s.cache.put(k, h)
	return h, nil
}

// revisionInfo is a revision with its commits, oldest first.
type revisionInfo struct {
	row     sqlcgen.Revision
	commits []string
}

func (s *Service) revisions(ctx context.Context, actor auth.User, projectName string, number int64) (sqlcgen.Review, string, []revisionInfo, error) {
	p, dir, err := s.project(ctx, actor, projectName)
	if err != nil {
		return sqlcgen.Review{}, "", nil, err
	}
	var r sqlcgen.Review
	var out []revisionInfo
	err = s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		var err error
		r, err = q.GetReviewByNumber(ctx, sqlcgen.GetReviewByNumberParams{ProjectID: p.ID, Number: number})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		revs, err := q.ListRevisions(ctx, r.ID)
		if err != nil {
			return err
		}
		for _, rv := range revs {
			commits, err := q.ListRevisionCommits(ctx, rv.ID)
			if err != nil {
				return err
			}
			info := revisionInfo{row: rv}
			for _, c := range commits {
				info.commits = append(info.commits, c.Sha)
			}
			out = append(out, info)
		}
		return nil
	})
	return r, dir, out, err
}

func resolveView(revs []revisionInfo, q DiffQuery) (from, to string, basesDiffer bool, err error) {
	byNumber := func(n int64) (revisionInfo, bool) {
		for _, r := range revs {
			if r.row.Number == n {
				return r, true
			}
		}
		return revisionInfo{}, false
	}
	switch {
	case q.Revision != 0:
		r, ok := byNumber(q.Revision)
		if !ok {
			return "", "", false, fmt.Errorf("%w: revision", ErrNotFound)
		}
		return r.row.BaseSha, r.row.HeadSha, false, nil
	case q.Commit != "":
		for _, r := range revs {
			if i := slices.Index(r.commits, q.Commit); i >= 0 {
				parent := r.row.BaseSha
				if i > 0 {
					parent = r.commits[i-1]
				}
				return parent, q.Commit, false, nil
			}
		}
		return "", "", false, fmt.Errorf("%w: commit", ErrNotFound)
	case q.FromRevision != 0 && q.ToRevision != 0:
		a, ok1 := byNumber(q.FromRevision)
		b, ok2 := byNumber(q.ToRevision)
		if !ok1 || !ok2 {
			return "", "", false, fmt.Errorf("%w: revision", ErrNotFound)
		}
		return a.row.HeadSha, b.row.HeadSha, a.row.BaseSha != b.row.BaseSha, nil
	case q.From != "" && q.To != "":
		for _, r := range revs {
			order := append([]string{r.row.BaseSha}, r.commits...)
			i, j := slices.Index(order, q.From), slices.Index(order, q.To)
			if i >= 0 && j > i {
				return q.From, q.To, false, nil
			}
		}
		return "", "", false, fmt.Errorf("%w: the range is not inside one revision", ErrInvalid)
	}
	return "", "", false, fmt.Errorf("%w: no view selected", ErrInvalid)
}

func knownCommits(revs []revisionInfo) map[string]bool {
	known := map[string]bool{}
	for _, r := range revs {
		known[r.row.BaseSha], known[r.row.HeadSha] = true, true
		for _, c := range r.commits {
			known[c] = true
		}
	}
	return known
}
