package gitrepo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// sourceRepo makes a working repository with main (two commits) and topic,
// as a member's local clone would be.
func sourceRepo(t *testing.T) (dir, main1, main2, topic string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "src")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	work(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	work(t, dir, "add", "a.txt")
	work(t, dir, "commit", "-q", "-m", "one")
	main1 = work(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	work(t, dir, "commit", "-q", "-am", "two")
	main2 = work(t, dir, "rev-parse", "HEAD")
	work(t, dir, "checkout", "-q", "-b", "topic", main1)
	work(t, dir, "commit", "-q", "--allow-empty", "-m", "topic")
	topic = work(t, dir, "rev-parse", "HEAD")
	return dir, main1, main2, topic
}

func TestSourceBranches(t *testing.T) {
	r := newRunner(t, 2)
	ctx := context.Background()
	src, main1, main2, topic := sourceRepo(t)
	// Remote-tracking branches, as a clone has them: origin/master exists
	// only there, origin/main collides with nothing, origin/HEAD is skipped.
	work(t, src, "update-ref", "refs/remotes/origin/master", main1)
	work(t, src, "update-ref", "refs/remotes/upstream/topic", main2)
	work(t, src, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/master")
	got, err := r.SourceBranches(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	want := []SourceBranch{
		{Ref: "refs/heads/main", From: "refs/heads/main", SHA: main2},
		{Ref: "refs/heads/origin/master", From: "refs/remotes/origin/master", SHA: main1},
		{Ref: "refs/heads/topic", From: "refs/heads/topic", SHA: topic},
		{Ref: "refs/heads/upstream/topic", From: "refs/remotes/upstream/topic", SHA: main2},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("branches\n got %v\nwant %v", got, want)
	}
	// A local branch named like a remote-tracking one wins.
	work(t, src, "branch", "origin/master", main2)
	got, _ = r.SourceBranches(ctx, src)
	if i := slices.IndexFunc(got, func(b SourceBranch) bool { return b.Ref == "refs/heads/origin/master" }); i < 0 || got[i].From != "refs/heads/origin/master" {
		t.Fatalf("local branch did not win: %v", got)
	}
	plain := t.TempDir()
	tests := []struct {
		name string
		path string
		want error
	}{
		{"relative", "src", ErrBadArgument},
		{"not clean", src + "/../src", ErrBadArgument},
		{"missing", filepath.Join(plain, "nope"), ErrNotRepository},
		{"not a repository", plain, ErrNotRepository},
		{"a file", filepath.Join(src, "a.txt"), ErrNotRepository},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := r.SourceBranches(ctx, tt.path); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestFetchBranchesAndMove(t *testing.T) {
	r := newRunner(t, 2)
	ctx := context.Background()
	src, main1, main2, topic := sourceRepo(t)
	bare := filepath.Join(t.TempDir(), "p.git")
	if err := r.InitBare(ctx, bare); err != nil {
		t.Fatal(err)
	}
	work(t, src, "update-ref", "refs/remotes/origin/master", main1)
	branches, err := r.SourceBranches(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.FetchBranches(ctx, bare, src, branches); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []SourceBranch{
		{Ref: "refs/heads/../x", From: "refs/heads/main"},
		{Ref: "refs/heads/x", From: "refs/tags/v1"},
		{Ref: "refs/heads/x", From: "refs/remotes/../x"},
	} {
		if err := r.FetchBranches(ctx, bare, src, []SourceBranch{bad}); !errors.Is(err, ErrBadArgument) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
	staged, err := r.AllRefs(ctx, bare)
	if err != nil {
		t.Fatal(err)
	}
	if staged[ImportRefPrefix+"main"] != main2 || staged[ImportRefPrefix+"topic"] != topic || staged[ImportRefPrefix+"origin/master"] != main1 {
		t.Fatalf("import refs %v", staged)
	}
	if ok, err := r.IsAncestor(ctx, bare, main1, main2); err != nil || !ok {
		t.Fatalf("main1 before main2: %v %v", ok, err)
	}
	if ok, err := r.IsAncestor(ctx, bare, topic, main2); err != nil || ok {
		t.Fatalf("topic before main2: %v %v", ok, err)
	}
	if err := r.SetBranch(ctx, bare, "refs/heads/main", main2, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.SetBranch(ctx, bare, "refs/heads/main", topic, main1); err == nil {
		t.Fatal("a stale old value moved the branch")
	}
	if err := r.SetBranch(ctx, bare, "refs/garret/x", topic, ""); !errors.Is(err, ErrBadArgument) {
		t.Fatalf("internal ref through SetBranch: %v", err)
	}
	if err := r.DeleteImportRef(ctx, bare, ImportRefPrefix+"main", main2); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteImportRef(ctx, bare, "refs/heads/main", main2); !errors.Is(err, ErrBadArgument) {
		t.Fatalf("branch through DeleteImportRef: %v", err)
	}
	refs, _ := r.AllRefs(ctx, bare)
	if _, ok := refs[ImportRefPrefix+"main"]; ok || refs["refs/heads/main"] != main2 {
		t.Fatalf("refs after %v", refs)
	}
}
