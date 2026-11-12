package review

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/0xmhha/garret/internal/gitrepo"
)

func TestDiffViews(t *testing.T) {
	f := newFixture(t)
	r := f.open(t)
	ctx := context.Background()
	d, _ := f.svc.Get(ctx, bob, "libfoo", r.Number)
	rev := d.Revisions[0]
	whole, err := f.svc.Diff(ctx, carol, "libfoo", r.Number, DiffQuery{Revision: 1})
	if err != nil || whole.From != rev.BaseSHA || whole.To != rev.HeadSHA || len(whole.Files) != 3 {
		t.Fatalf("revision view %+v, %v", whole, err)
	}
	one, err := f.svc.Diff(ctx, carol, "libfoo", r.Number, DiffQuery{Commit: rev.Commits[1].SHA})
	if err != nil || one.From != rev.Commits[0].SHA || len(one.Files) != 1 || one.Files[0].NewPath != "b.txt" {
		t.Fatalf("commit view %+v, %v", one, err)
	}
	rng, err := f.svc.Diff(ctx, carol, "libfoo", r.Number, DiffQuery{From: rev.Commits[0].SHA, To: rev.Commits[2].SHA})
	if err != nil || len(rng.Files) != 2 {
		t.Fatalf("range view %+v, %v", rng, err)
	}
	for name, q := range map[string]DiffQuery{
		"nothing":        {},
		"unknown rev":    {Revision: 9},
		"foreign range":  {From: rev.Commits[2].SHA, To: rev.Commits[0].SHA},
		"foreign commit": {Commit: strings.Repeat("a", 40)},
	} {
		if _, err := f.svc.Diff(ctx, carol, "libfoo", r.Number, q); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	hunks, err := f.svc.FileDiff(ctx, carol, "libfoo", r.Number, rev.BaseSHA, rev.HeadSHA, "a.txt", "a.txt")
	if err != nil || len(hunks) != 1 {
		t.Fatalf("FileDiff = %+v, %v", hunks, err)
	}
	if _, err := f.svc.FileDiff(ctx, carol, "libfoo", r.Number, strings.Repeat("b", 40), rev.HeadSHA, "a.txt", "a.txt"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("commit outside the review: %v", err)
	}
	if n, _ := f.svc.cache.stats(); n != 1 {
		t.Fatalf("cache holds %d entries", n)
	}
}

func TestRevisionPairNoticesRebase(t *testing.T) {
	f := newFixture(t)
	r := f.open(t)
	ctx := context.Background()
	f.run(t, "checkout", "-q", "main")
	f.commit(t, "upstream.txt", "u\n", "upstream")
	f.run(t, "push", "-q", f.repo, "main")
	f.run(t, "checkout", "-q", "fix")
	f.run(t, "rebase", "-q", "main")
	head := f.run(t, "rev-parse", "HEAD")
	f.run(t, "push", "-q", "-f", f.repo, "fix")
	if err := f.svc.HandlePush(ctx, "libfoo", bob, []RefChange{{Ref: "refs/heads/fix", New: head}}); err != nil {
		t.Fatal(err)
	}
	pair, err := f.svc.Diff(ctx, carol, "libfoo", r.Number, DiffQuery{FromRevision: 1, ToRevision: 2})
	if err != nil || !pair.BasesDiffer {
		t.Fatalf("pair %+v, %v", pair, err)
	}
}

func TestDiffCacheEviction(t *testing.T) {
	c := newDiffCache()
	for i := range cacheEntries + 10 {
		c.put(cacheKey{from: fmt.Sprint(i)}, []gitrepo.Hunk{{Lines: []gitrepo.Line{{Text: []byte("x")}}}})
	}
	if n, _ := c.stats(); n != cacheEntries {
		t.Fatalf("%d entries, want %d", n, cacheEntries)
	}
	if _, ok := c.get(cacheKey{from: "0"}); ok {
		t.Fatal("oldest entry not evicted")
	}
	big := []gitrepo.Hunk{{Lines: []gitrepo.Line{{Text: make([]byte, cacheBytes+1)}}}}
	c.put(cacheKey{from: "big"}, big)
	if _, ok := c.get(cacheKey{from: "big"}); ok {
		t.Fatal("an entry over the byte limit was cached")
	}
	if _, b := c.stats(); b > cacheBytes {
		t.Fatalf("%d bytes cached", b)
	}
}

// anchorComment inserts an unresolved code comment on revision 1.
func (f *fixture) anchorComment(t *testing.T, r Review, rev Revision, path, side string, start, end int, resolved int) string {
	t.Helper()
	id := fmt.Sprintf("01JD00000000000000000C%04d", start*10+end+resolved*1000)
	ctx := context.Background()
	err := f.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var s, e any
		if start > 0 {
			s, e = start, end
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO comments (id, project_id, review_id, author_id, anchor_revision_id,
			anchor_view, anchor_from_sha, anchor_to_sha, anchor_path, anchor_side, anchor_start_line, anchor_end_line,
			resolved, current_version, created_at) VALUES (?, ?, ?, ?, ?, 'revision', ?, ?, ?, ?, ?, ?, ?, 1, 1)`,
			id, r.ProjectID, r.ID, carol.ID, rev.ID, rev.BaseSHA, rev.HeadSHA, path, side, s, e, resolved)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPortAnchors(t *testing.T) {
	f := newFixture(t)
	r := f.open(t)
	ctx := context.Background()
	d, _ := f.svc.Get(ctx, bob, "libfoo", r.Number)
	rev := d.Revisions[0]
	// a.txt on revision 1 is "one\nTWO\nthree\n"; b.txt is "b\n".
	moved := f.anchorComment(t, r, rev, "a.txt", "new", 3, 3, 0)
	touched := f.anchorComment(t, r, rev, "a.txt", "new", 2, 2, 0)
	resolved := f.anchorComment(t, r, rev, "a.txt", "new", 1, 1, 1)
	gone := f.anchorComment(t, r, rev, "b.txt", "new", 1, 1, 0)
	f.run(t, "rm", "-q", "b.txt")
	head := f.commit(t, "a.txt", "zero\none\nTWO!\nthree\n", "fourth")
	f.run(t, "push", "-q", f.repo, "fix")
	if err := f.svc.HandlePush(ctx, "libfoo", bob, []RefChange{{Ref: "refs/heads/fix", New: head}}); err != nil {
		t.Fatal(err)
	}
	ported, err := f.svc.PortAnchors(ctx, carol, "libfoo", r.Number, 2)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]PortedAnchor{}
	for _, p := range ported {
		byID[p.CommentID] = p
	}
	if p := byID[moved].Position; p == nil || p.Start != 4 || p.End != 4 || p.FileLevel {
		t.Errorf("unchanged line should move down by one: %+v", p)
	}
	if p := byID[touched].Position; p == nil || !p.FileLevel {
		t.Errorf("changed line should become file level: %+v", p)
	}
	if p := byID[resolved].Position; p != nil {
		t.Errorf("resolved comment was ported: %+v", p)
	}
	if p := byID[gone].Position; p == nil || !p.Dropped {
		t.Errorf("comment on a deleted file: %+v", p)
	}
	same, err := f.svc.PortAnchors(ctx, carol, "libfoo", r.Number, 1)
	if err != nil || len(same) != 4 || same[0].Position != nil {
		t.Fatalf("on its own revision: %+v, %v", same, err)
	}
}

func TestReviewNotifications(t *testing.T) {
	f := newFixture(t)
	r := f.open(t)
	ctx := context.Background()
	if err := f.svc.RecordVerdict(ctx, carol, "libfoo", r.Number, VerdictRequestChanges); err != nil {
		t.Fatal(err)
	}
	head := f.commit(t, "d.txt", "d\n", "fourth")
	f.run(t, "push", "-q", f.repo, "fix")
	if err := f.svc.HandlePush(ctx, "libfoo", bob, []RefChange{{Ref: "refs/heads/fix", New: head}}); err != nil {
		t.Fatal(err)
	}
	count := func(user, kind string) int {
		var n int
		_ = f.db.ReadTx(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, "SELECT count(*) FROM notifications WHERE user_id = ? AND kind = ?", user, kind).Scan(&n)
		})
		return n
	}
	if count(bob.ID, "verdict_on_my_review") != 1 {
		t.Error("review author not notified of the verdict")
	}
	if count(carol.ID, "revision_on_reviewed") != 1 {
		t.Error("reviewer not notified of the new revision")
	}
	if count(bob.ID, "revision_on_reviewed") != 0 {
		t.Error("pusher notified of their own revision")
	}
}
