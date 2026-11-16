package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// ImportRefPrefix is where FetchBranches stages a source's branches before
// they are compared with the project's (architecture.md, "Importing from
// the server's machine").
const ImportRefPrefix = internalRefPrefix + "import/"

// ErrNotRepository reports a source path that is not a Git repository.
var ErrNotRepository = errors.New("gitrepo: not a git repository")

// notAncestor is git merge-base --is-ancestor's exit status for "no".
const notAncestor = 1

// remotePrefix is where a clone keeps its remote-tracking branches.
const remotePrefix = "refs/remotes/"

// SourceBranch is a branch of a source repository as a project receives
// it: Ref is the project's branch, From the source ref it comes from.
type SourceBranch struct {
	Ref  string
	From string
	SHA  string
}

// SourceBranches returns the branches of the repository at path, a working
// tree or a bare repository on this machine, sorted by Ref: every local
// branch under its own name, and every remote-tracking branch as
// <remote>/<name> unless a local branch has that name (decisions.md D15).
// <remote>/HEAD and names garret would refuse in a push are left out. path
// must be absolute and clean.
func (r *Runner) SourceBranches(ctx context.Context, path string) ([]SourceBranch, error) {
	if err := checkSourcePath(path); err != nil {
		return nil, err
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrNotRepository, path)
	}
	if _, err := r.run(ctx, false, "-C", path, "rev-parse", "--git-dir"); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotRepository, path)
	}
	out, err := r.run(ctx, false, "-C", path, "for-each-ref", "--format=%(objectname) %(refname)", "refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	local := map[string]SourceBranch{}
	remote := map[string]SourceBranch{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		sha, ref, ok := strings.Cut(line, " ")
		if !ok || !isObjectName(sha) {
			return nil, fmt.Errorf("%w: for-each-ref line %q", ErrRawFormat, line)
		}
		b, ok := sourceBranch(ref, sha)
		switch {
		case !ok:
		case strings.HasPrefix(ref, branchPrefix):
			local[b.Ref] = b
		default:
			remote[b.Ref] = b
		}
	}
	for ref, b := range remote {
		if _, taken := local[ref]; !taken {
			local[ref] = b
		}
	}
	branches := make([]SourceBranch, 0, len(local))
	for _, b := range local {
		branches = append(branches, b)
	}
	slices.SortFunc(branches, func(a, b SourceBranch) int { return strings.Compare(a.Ref, b.Ref) })
	return branches, nil
}

// sourceBranch maps a source ref to the project branch it becomes, or
// false for a ref that is not imported.
func sourceBranch(ref, sha string) (SourceBranch, bool) {
	var name string
	switch {
	case strings.HasPrefix(ref, branchPrefix):
		name = strings.TrimPrefix(ref, branchPrefix)
	case strings.HasPrefix(ref, remotePrefix):
		name = strings.TrimPrefix(ref, remotePrefix)
		if _, rest, ok := strings.Cut(name, "/"); !ok || rest == "HEAD" {
			return SourceBranch{}, false
		}
	default:
		return SourceBranch{}, false
	}
	b := SourceBranch{Ref: branchPrefix + name, From: ref, SHA: sha}
	return b, ValidBranchRef(b.Ref) == nil
}

// FetchBranches copies the given branches of the repository at source into
// dir under ImportRefPrefix, each under its project name. Nothing runs in
// the source; it is only read.
func (r *Runner) FetchBranches(ctx context.Context, dir, source string, branches []SourceBranch) error {
	if err := checkSourcePath(source); err != nil {
		return err
	}
	if len(branches) == 0 {
		return nil
	}
	args := []string{"--git-dir", dir, "fetch", "--no-tags", "--quiet", "--no-write-fetch-head", "--", source}
	for _, b := range branches {
		if want, ok := sourceBranch(b.From, b.SHA); !ok || want.Ref != b.Ref {
			return fmt.Errorf("%w: fetch %q as %q", ErrBadArgument, b.From, b.Ref)
		}
		args = append(args, "+"+b.From+":"+ImportRefPrefix+strings.TrimPrefix(b.Ref, branchPrefix))
	}
	_, err := r.run(ctx, true, args...)
	return err
}

// IsAncestor reports whether commit a is an ancestor of commit b (or b
// itself), so moving a branch from a to b is a fast-forward.
func (r *Runner) IsAncestor(ctx context.Context, dir, a, b string) (bool, error) {
	if !isObjectName(a) || !isObjectName(b) {
		return false, fmt.Errorf("%w: merge-base", ErrBadArgument)
	}
	_, err := r.run(ctx, false, "--git-dir", dir, "merge-base", "--is-ancestor", a, b)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == notAncestor {
		return false, nil
	}
	return err == nil, err
}

// SetBranch points the branch ref at newSHA if it currently points at
// oldSHA; an empty oldSHA requires that the branch does not exist yet.
func (r *Runner) SetBranch(ctx context.Context, dir, ref, newSHA, oldSHA string) error {
	if ValidBranchRef(ref) != nil || !isObjectName(newSHA) || (oldSHA != "" && !isObjectName(oldSHA)) {
		return fmt.Errorf("%w: update-ref", ErrBadArgument)
	}
	if oldSHA == "" {
		oldSHA = strings.Repeat("0", len(newSHA))
	}
	_, err := r.run(ctx, false, "--git-dir", dir, "update-ref", ref, newSHA, oldSHA)
	return err
}

// DeleteImportRef removes a ref under ImportRefPrefix if it points at oldSHA.
func (r *Runner) DeleteImportRef(ctx context.Context, dir, ref, oldSHA string) error {
	if !strings.HasPrefix(ref, ImportRefPrefix) || !validInternalRef(ref) || !isObjectName(oldSHA) {
		return fmt.Errorf("%w: update-ref -d", ErrBadArgument)
	}
	_, err := r.run(ctx, false, "--git-dir", dir, "update-ref", "-d", ref, oldSHA)
	return err
}

// checkSourcePath accepts only absolute, clean paths, so a typed path
// cannot be read as an option or wander through "..".
func checkSourcePath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("%w: source path must be absolute and clean", ErrBadArgument)
	}
	return nil
}
