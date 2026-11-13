package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// SourceBranches returns the branch heads of the repository at path, a
// working tree or a bare repository on this machine. path must be absolute
// and clean. Branch names garret would refuse in a push are left out.
func (r *Runner) SourceBranches(ctx context.Context, path string) (map[string]string, error) {
	if err := checkSourcePath(path); err != nil {
		return nil, err
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrNotRepository, path)
	}
	if _, err := r.run(ctx, false, "-C", path, "rev-parse", "--git-dir"); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotRepository, path)
	}
	out, err := r.run(ctx, false, "-C", path, "for-each-ref", "--format=%(objectname) %(refname)", "refs/heads")
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		sha, ref, ok := strings.Cut(line, " ")
		if !ok || !isObjectName(sha) {
			return nil, fmt.Errorf("%w: for-each-ref line %q", ErrRawFormat, line)
		}
		if ValidBranchRef(ref) == nil {
			refs[ref] = sha
		}
	}
	return refs, nil
}

// FetchBranches copies the given branches (full refs/heads/ names, as
// SourceBranches returns them) of the repository at source into dir under
// ImportRefPrefix. Nothing runs in the source; it is only read.
func (r *Runner) FetchBranches(ctx context.Context, dir, source string, branches []string) error {
	if err := checkSourcePath(source); err != nil {
		return err
	}
	if len(branches) == 0 {
		return nil
	}
	args := []string{"--git-dir", dir, "fetch", "--no-tags", "--quiet", "--no-write-fetch-head", "--", source}
	for _, b := range branches {
		if ValidBranchRef(b) != nil {
			return fmt.Errorf("%w: fetch %q", ErrBadArgument, b)
		}
		args = append(args, "+"+b+":"+ImportRefPrefix+strings.TrimPrefix(b, branchPrefix))
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
