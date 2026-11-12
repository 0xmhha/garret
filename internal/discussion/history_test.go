package discussion

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestHistory(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	d, err := s.Create(ctx, bob, "libfoo", "Disclosure plan")
	if err != nil {
		t.Fatal(err)
	}
	if h, next, err := s.History(ctx, carol, "libfoo", d.ID, 0, 10); err != nil || len(h) != 0 || next != 0 {
		t.Fatalf("empty history %v %d %v", h, next, err)
	}
	texts := []string{"draft", "draft and plan", "final plan"}
	if err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		for i, text := range texts {
			if _, err := tx.ExecContext(ctx, "INSERT INTO collab_checkpoints VALUES (?, ?, x'00', ?, ?, ?)",
				d.BodyDocID, (i+1)*5, text, `["`+bob.ID+`","`+carol.ID+`"]`, int64(1000*(i+1))); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page, next, err := s.History(ctx, carol, "libfoo", d.ID, 0, 2)
	if err != nil || len(page) != 2 || page[0].Text != "draft" || page[1].UpToSeq != 10 || next != 10 {
		t.Fatalf("first page %+v next %d %v", page, next, err)
	}
	if len(page[0].Contributors) != 2 || page[0].Contributors[1] != carol.ID || !page[0].At.Equal(time.UnixMilli(1000).UTC()) {
		t.Fatalf("checkpoint %+v", page[0])
	}
	rest, next, err := s.History(ctx, carol, "libfoo", d.ID, next, 2)
	if err != nil || len(rest) != 1 || rest[0].Text != "final plan" || next != 0 {
		t.Fatalf("second page %+v next %d %v", rest, next, err)
	}
	for _, limit := range []int{0, 101} {
		if _, _, err := s.History(ctx, carol, "libfoo", d.ID, 0, limit); !errors.Is(err, ErrInvalid) {
			t.Errorf("limit %d: %v", limit, err)
		}
	}
	if _, _, err := s.History(ctx, carol, "libfoo", "01JD000000000000000MISSING", 0, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown discussion: %v", err)
	}
	if err := s.Delete(ctx, bob, "libfoo", d.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.History(ctx, carol, "libfoo", d.ID, 0, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted discussion: %v", err)
	}
}
