package audit

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/store"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

var at = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

func setup(t *testing.T) (*store.DB, *Recorder) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "garret.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ids, err := id.NewGenerator(fixedClock{at}, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return db, NewRecorder(ids, fixedClock{at})
}

func TestRecord(t *testing.T) {
	db, r := setup(t)
	err := db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		return r.Record(context.Background(), tx, Event{
			Action: "user.created", SubjectKind: "user", SubjectID: "U1",
			Data: map[string]any{"role": "admin", "b": 1},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	var action, data string
	var project, actor sql.NullString
	var created int64
	err = db.ReadTx(context.Background(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(context.Background(),
			"SELECT action, data, project_id, actor_id, created_at FROM audit_events").Scan(&action, &data, &project, &actor, &created)
	})
	if err != nil {
		t.Fatal(err)
	}
	if action != "user.created" || data != `{"b":1,"role":"admin"}` || project.Valid || actor.Valid || created != at.UnixMilli() {
		t.Fatalf("row: %s %s %v %v %d", action, data, project, actor, created)
	}
}

func TestRecordRollsBackWithCaller(t *testing.T) {
	db, r := setup(t)
	errStop := errors.New("stop")
	err := db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		if err := r.Record(context.Background(), tx, Event{Action: "x.y", SubjectKind: "k", SubjectID: "1"}); err != nil {
			return err
		}
		return errStop
	})
	if !errors.Is(err, errStop) {
		t.Fatal(err)
	}
	var n int
	_ = db.ReadTx(context.Background(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(context.Background(), "SELECT count(*) FROM audit_events").Scan(&n)
	})
	if n != 0 {
		t.Fatalf("%d events after rollback", n)
	}
}

func TestRecordValidates(t *testing.T) {
	db, r := setup(t)
	tests := []Event{
		{Action: "created", SubjectKind: "k", SubjectID: "1"},
		{Action: "User.Created", SubjectKind: "k", SubjectID: "1"},
		{Action: "a.b", SubjectKind: "", SubjectID: "1"},
		{Action: "a.b", SubjectKind: "k", SubjectID: ""},
		{Action: "a.b", SubjectKind: "k", SubjectID: "1", Data: map[string]any{"f": func() {}}},
	}
	for _, e := range tests {
		err := db.WriteTx(context.Background(), func(tx *sql.Tx) error { return r.Record(context.Background(), tx, e) })
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("Record(%+v) = %v, want ErrInvalid", e, err)
		}
	}
}
