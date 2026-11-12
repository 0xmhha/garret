package discussion

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/store/sqlcgen"
)

// MaxHistoryPage is the largest page History returns.
const MaxHistoryPage = 100

// Checkpoint is the discussion body's text at one point, with the users
// whose edits it contains.
type Checkpoint struct {
	UpToSeq      int64
	Text         string
	Contributors []string
	At           time.Time
}

// History returns up to limit checkpoints of the discussion's body after
// sequence number after, oldest first, and the cursor of the next page (0
// at the end). A deleted discussion is ErrNotFound.
func (s *Service) History(ctx context.Context, _ auth.User, projectName, discussionID string, after int64, limit int) ([]Checkpoint, int64, error) {
	if limit < 1 || limit > MaxHistoryPage {
		return nil, 0, fmt.Errorf("%w: limit must be 1 to %d", ErrInvalid, MaxHistoryPage)
	}
	var rows []sqlcgen.ListCheckpointsAfterRow
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		d, err := lookup(ctx, q, projectName, discussionID)
		if err != nil {
			return err
		}
		// One extra row tells whether another page follows.
		rows, err = q.ListCheckpointsAfter(ctx, sqlcgen.ListCheckpointsAfterParams{DocumentID: d.BodyDocID, After: after, MaxRows: int64(limit) + 1})
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	var next int64
	if len(rows) > limit {
		rows = rows[:limit]
		next = rows[limit-1].UpToSeq
	}
	out := make([]Checkpoint, len(rows))
	for i, r := range rows {
		var who []string
		if err := json.Unmarshal([]byte(r.Contributors), &who); err != nil {
			return nil, 0, fmt.Errorf("discussion: checkpoint %d: %w", r.UpToSeq, err)
		}
		out[i] = Checkpoint{UpToSeq: r.UpToSeq, Text: r.Text, Contributors: who, At: time.UnixMilli(r.CreatedAt).UTC()}
	}
	return out, next, nil
}
