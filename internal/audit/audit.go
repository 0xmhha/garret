package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/0xmhha/garret/internal/clock"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/store/sqlcgen"
)

// ErrInvalid is returned for an event that does not have the required form.
var ErrInvalid = errors.New("audit: invalid event")

var actionPattern = regexp.MustCompile(`^[a-z][a-z_]*(\.[a-z][a-z_]*)+$`)

// Event is one audit record. ProjectID and ActorID are empty for
// server-level and system events. Data must never hold secrets or content
// bodies; it is stored as a JSON object with sorted keys.
type Event struct {
	ProjectID   string
	ActorID     string
	Action      string // dotted, for example "review.created"
	SubjectKind string
	SubjectID   string
	Data        map[string]any
}

// Recorder writes audit events. It is safe for concurrent use.
type Recorder struct {
	ids   *id.Generator
	clock clock.Clock
}

// NewRecorder returns a Recorder that stamps events with ids and c.
func NewRecorder(ids *id.Generator, c clock.Clock) *Recorder {
	return &Recorder{ids: ids, clock: c}
}

// Record inserts e inside tx, so it commits or rolls back with the change it
// describes. It returns ErrInvalid for a malformed event.
func (r *Recorder) Record(ctx context.Context, tx *sql.Tx, e Event) error {
	if !actionPattern.MatchString(e.Action) || e.SubjectKind == "" || e.SubjectID == "" {
		return fmt.Errorf("%w: action %q, subject %q/%q", ErrInvalid, e.Action, e.SubjectKind, e.SubjectID)
	}
	data := e.Data
	if data == nil {
		data = map[string]any{}
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("%w: data: %w", ErrInvalid, err)
	}
	eventID, err := r.ids.New()
	if err != nil {
		return err
	}
	return sqlcgen.New(tx).InsertAuditEvent(ctx, sqlcgen.InsertAuditEventParams{
		ID:          eventID,
		ProjectID:   nullable(e.ProjectID),
		ActorID:     nullable(e.ActorID),
		Action:      e.Action,
		SubjectKind: e.SubjectKind,
		SubjectID:   e.SubjectID,
		Data:        string(encoded),
		CreatedAt:   r.clock.Now().UnixMilli(),
	})
}

func nullable(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
