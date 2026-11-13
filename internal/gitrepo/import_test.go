package gitrepo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	src, _, main2, topic := sourceRepo(t)
	got, err := r.SourceBranches(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["refs/heads/main"] != main2 || got["refs/heads/topic"] != topic {
		t.Fatalf("branches %v", got)
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
	if err := r.FetchBranches(ctx, bare, src, []string{"refs/heads/main", "refs/heads/topic"}); err != nil {
		t.Fatal(err)
	}
	if err := r.FetchBranches(ctx, bare, src, []string{"refs/heads/../x"}); !errors.Is(err, ErrBadArgument) {
		t.Fatalf("bad branch name: %v", err)
	}
	staged, err := r.AllRefs(ctx, bare)
	if err != nil {
		t.Fatal(err)
	}
	if staged[ImportRefPrefix+"main"] != main2 || staged[ImportRefPrefix+"topic"] != topic {
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
