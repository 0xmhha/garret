package review

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/0xmhha/garret/internal/gitrepo"
)

func TestFile(t *testing.T) {
	f := newFixture(t)
	r := f.open(t)
	ctx := context.Background()
	d, _ := f.svc.Get(ctx, bob, "libfoo", r.Number)
	rev := d.Revisions[0]
	got, err := f.svc.File(ctx, carol, "libfoo", r.Number, rev.HeadSHA, "a.txt")
	if err != nil || string(got) != "one\nTWO\nthree\n" {
		t.Fatalf("File at head = %q, %v", got, err)
	}
	if got, err := f.svc.File(ctx, carol, "libfoo", r.Number, rev.BaseSHA, "a.txt"); err != nil || string(got) != "one\ntwo\nthree\n" {
		t.Fatalf("File at base = %q, %v", got, err)
	}
	for _, p := range []string{"", "/a.txt", "../a.txt", "./a.txt", "x/../a.txt", "a.txt/.", "a\x00.txt"} {
		if _, err := f.svc.File(ctx, carol, "libfoo", r.Number, rev.HeadSHA, p); !errors.Is(err, ErrInvalid) {
			t.Errorf("path %q: %v", p, err)
		}
	}
	if _, err := f.svc.File(ctx, carol, "libfoo", r.Number, strings.Repeat("a", 40), "a.txt"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("commit outside the review: %v", err)
	}
	if _, err := f.svc.File(ctx, carol, "libfoo", r.Number, rev.HeadSHA, "missing.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing file: %v", err)
	}
	if _, err := f.svc.File(ctx, carol, "libfoo", 99, rev.HeadSHA, "a.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown review: %v", err)
	}

	old := rev.HeadSHA
	bin := f.commit(t, "bin.dat", "a\x00b", "binary")
	big := f.commit(t, "big.txt", strings.Repeat("x", 3<<20), "big")
	f.run(t, "push", "-q", f.repo, "fix")
	if err := f.svc.HandlePush(ctx, "libfoo", bob, []RefChange{{Ref: "refs/heads/fix", Old: old, New: big}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.File(ctx, carol, "libfoo", r.Number, bin, "bin.dat"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("binary file: %v", err)
	}
	if _, err := f.svc.File(ctx, carol, "libfoo", r.Number, big, "big.txt"); !errors.Is(err, gitrepo.ErrTooLarge) {
		t.Fatalf("file over the limit: %v", err)
	}
}

func TestHistory(t *testing.T) {
	f := newFixture(t)
	r := f.open(t)
	ctx := context.Background()
	// The first revision comes with review.created; a push adds a second.
	old := f.run(t, "rev-parse", "HEAD")
	head := f.commit(t, "e.txt", "e\n", "fourth")
	f.run(t, "push", "-q", f.repo, "fix")
	if err := f.svc.HandlePush(ctx, "libfoo", bob, []RefChange{{Ref: "refs/heads/fix", Old: old, New: head}}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RecordVerdict(ctx, carol, "libfoo", r.Number, VerdictApprove); err != nil {
		t.Fatal(err)
	}
	// Another review's events stay out.
	f.run(t, "checkout", "-q", "main")
	f.run(t, "checkout", "-q", "-b", "other")
	f.commit(t, "d.txt", "d\n", "other")
	f.run(t, "push", "-q", f.repo, "other")
	if _, err := f.svc.Open(ctx, carol, "libfoo", "refs/heads/other", "refs/heads/main", "Other"); err != nil {
		t.Fatal(err)
	}

	all, next, err := f.svc.History(ctx, carol, "libfoo", r.Number, "", 100)
	if err != nil || next != "" {
		t.Fatalf("History = %v, next %q", err, next)
	}
	var actions []string
	for _, e := range all {
		actions = append(actions, e.Action)
	}
	if strings.Join(actions, ",") != "review.created,revision.created,verdict.recorded" {
		t.Fatalf("actions %v", actions)
	}
	if all[2].ActorID != carol.ID || all[2].Data["value"] != string(VerdictApprove) {
		t.Fatalf("verdict entry %+v", all[2])
	}
	page, next, err := f.svc.History(ctx, carol, "libfoo", r.Number, "", 2)
	if err != nil || len(page) != 2 || next != page[1].ID {
		t.Fatalf("first page %d next %q %v", len(page), next, err)
	}
	rest, next, err := f.svc.History(ctx, carol, "libfoo", r.Number, next, 2)
	if err != nil || len(rest) != 1 || rest[0].Action != "verdict.recorded" || next != "" {
		t.Fatalf("second page %+v next %q %v", rest, next, err)
	}
	for _, limit := range []int{0, 101} {
		if _, _, err := f.svc.History(ctx, carol, "libfoo", r.Number, "", limit); !errors.Is(err, ErrInvalid) {
			t.Errorf("limit %d: %v", limit, err)
		}
	}
	if _, _, err := f.svc.History(ctx, carol, "libfoo", 99, "", 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown review: %v", err)
	}
}
