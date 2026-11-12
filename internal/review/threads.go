package review

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/gitrepo"
	"github.com/0xmhha/garret/internal/store/sqlcgen"
)

// PortedPosition is where an anchored comment appears on a later revision.
type PortedPosition struct {
	Path       string
	Side       string
	Start, End int
	FileLevel  bool // the commented lines changed; shown on the file
	Dropped    bool // the file was deleted; shown only on its own revision
}

// PortedAnchor ties a code comment to its position on the requested
// revision. Position is nil when the comment is shown where it was written.
type PortedAnchor struct {
	CommentID        string
	AnchorRevisionNo int64
	Position         *PortedPosition
}

// PortAnchors returns, for every unresolved anchored root comment written on
// an earlier revision than atRevision, its position on atRevision. Resolved
// comments stay on their own revision (data-model.md, Comment porting).
// Porting is computed on read and never stored.
func (s *Service) PortAnchors(ctx context.Context, actor auth.User, projectName string, number, atRevision int64) ([]PortedAnchor, error) {
	r, dir, revs, err := s.revisions(ctx, actor, projectName, number)
	if err != nil {
		return nil, err
	}
	var target *revisionInfo
	noByID := map[string]int64{}
	for i := range revs {
		noByID[revs[i].row.ID] = revs[i].row.Number
		if revs[i].row.Number == atRevision {
			target = &revs[i]
		}
	}
	if target == nil {
		return nil, fmt.Errorf("%w: revision", ErrNotFound)
	}
	var rows []sqlcgen.ListReviewCommentsRow
	err = s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		rows, err = sqlcgen.New(tx).ListReviewComments(ctx, sql.NullString{String: r.ID, Valid: true})
		return err
	})
	if err != nil {
		return nil, err
	}
	var out []PortedAnchor
	for _, c := range rows {
		if !c.AnchorPath.Valid || c.ThreadRootID.Valid || c.DeletedAt.Valid {
			continue
		}
		anchorNo := noByID[c.AnchorRevisionID.String]
		pa := PortedAnchor{CommentID: c.ID, AnchorRevisionNo: anchorNo}
		if anchorNo < atRevision && c.Resolved == 0 {
			pos, err := s.port(ctx, dir, c, target.row)
			if err != nil {
				return nil, err
			}
			pa.Position = &pos
		}
		out = append(out, pa)
	}
	return out, nil
}

// port moves one anchor to the target revision: new-side anchors follow the
// diff from the anchored commit to the target head, old-side anchors the
// diff to the target base.
func (s *Service) port(ctx context.Context, dir string, c sqlcgen.ListReviewCommentsRow, target sqlcgen.Revision) (PortedPosition, error) {
	src, dst := c.AnchorToSha.String, target.HeadSha
	if c.AnchorSide.String == "old" {
		src, dst = c.AnchorFromSha.String, target.BaseSha
	}
	pos := PortedPosition{Path: c.AnchorPath.String, Side: c.AnchorSide.String,
		Start: int(c.AnchorStartLine.Int64), End: int(c.AnchorEndLine.Int64)}
	if src == dst {
		return pos, nil
	}
	files, err := s.runner.DiffSummary(ctx, dir, src, dst)
	if err != nil {
		return PortedPosition{}, err
	}
	var change *gitrepo.FileDiff
	for i := range files {
		if files[i].OldPath == pos.Path {
			change = &files[i]
			break
		}
	}
	if change == nil {
		return pos, nil // the file did not change
	}
	if change.Status == gitrepo.StatusDeleted {
		return PortedPosition{Path: pos.Path, Side: pos.Side, Dropped: true}, nil
	}
	pos.Path = change.NewPath
	if pos.Start == 0 {
		return pos, nil // file-level comments move with the file
	}
	if change.Binary {
		return PortedPosition{Path: pos.Path, Side: pos.Side, FileLevel: true}, nil
	}
	hunks, err := s.fileHunks(ctx, dir, src, dst, change.OldPath, change.NewPath)
	if errors.Is(err, gitrepo.ErrTooLarge) {
		return PortedPosition{Path: pos.Path, Side: pos.Side, FileLevel: true}, nil
	}
	if err != nil {
		return PortedPosition{}, err
	}
	p, err := Port(ChangeBlocks(hunks), pos.Start, pos.End)
	if err != nil {
		return PortedPosition{}, err
	}
	if p.FileLevel {
		return PortedPosition{Path: pos.Path, Side: pos.Side, FileLevel: true}, nil
	}
	pos.Start, pos.End = p.Start, p.End
	return pos, nil
}
