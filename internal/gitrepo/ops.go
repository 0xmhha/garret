package gitrepo

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
)

// repoConfig is applied to every new bare repository
// (docs/design/architecture.md, "Serving the protocol").
var repoConfig = [][2]string{
	{"uploadpack.hideRefs", "refs/garret"},
	{"receive.hideRefs", "refs/garret"},
	{"receive.denyDeleteCurrent", "true"},
}

const (
	shaLen1   = 40
	shaLen256 = 64
	// logFields is the number of NUL-separated fields per commit in RevList.
	logFields = 4
)

// InitBare creates a bare repository at dir with garret's settings.
func (r *Runner) InitBare(ctx context.Context, dir string) error {
	if _, err := r.run(ctx, false, "init", "--bare", "--quiet", "--", dir); err != nil {
		return err
	}
	for _, kv := range repoConfig {
		if _, err := r.run(ctx, false, "--git-dir", dir, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

// Refs returns the branch heads of the repository at dir, by full ref name.
func (r *Runner) Refs(ctx context.Context, dir string) (map[string]string, error) {
	out, err := r.run(ctx, false, "--git-dir", dir, "for-each-ref", "--format=%(objectname) %(refname)", "refs/heads")
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
		refs[ref] = sha
	}
	return refs, nil
}

// AllRefs returns every ref of the repository at dir, hidden ones included:
// the same set a bundle of --all contains.
func (r *Runner) AllRefs(ctx context.Context, dir string) (map[string]string, error) {
	out, err := r.run(ctx, false, "--git-dir", dir, "for-each-ref", "--format=%(objectname) %(refname)")
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
		refs[ref] = sha
	}
	return refs, nil
}

// UpdateRef points ref, which must be under refs/garret/, at newSHA if it
// currently points at oldSHA; an empty oldSHA requires that ref does not
// exist yet.
func (r *Runner) UpdateRef(ctx context.Context, dir, ref, newSHA, oldSHA string) error {
	if !validInternalRef(ref) || !isObjectName(newSHA) || (oldSHA != "" && !isObjectName(oldSHA)) {
		return fmt.Errorf("%w: update-ref", ErrBadArgument)
	}
	if oldSHA == "" {
		oldSHA = strings.Repeat("0", len(newSHA))
	}
	_, err := r.run(ctx, false, "--git-dir", dir, "update-ref", ref, newSHA, oldSHA)
	return err
}

// MergeBase returns the best common ancestor of commits a and b.
func (r *Runner) MergeBase(ctx context.Context, dir, a, b string) (string, error) {
	if !isObjectName(a) || !isObjectName(b) {
		return "", fmt.Errorf("%w: merge-base", ErrBadArgument)
	}
	out, err := r.run(ctx, false, "--git-dir", dir, "merge-base", a, b)
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(out))
	if !isObjectName(sha) {
		return "", fmt.Errorf("%w: merge-base output", ErrRawFormat)
	}
	return sha, nil
}

// Commit is one commit of a revision, as listed by RevList.
type Commit struct {
	SHA        string
	AuthorName string
	AuthoredAt int64 // Unix seconds
	Subject    string
}

// RevList returns the commits in base..head, oldest first. If there are more
// than max, it returns no commits and overflow true.
func (r *Runner) RevList(ctx context.Context, dir, base, head string, max int) ([]Commit, bool, error) {
	if !isObjectName(base) || !isObjectName(head) || max < 1 {
		return nil, false, fmt.Errorf("%w: rev-list", ErrBadArgument)
	}
	out, err := r.run(ctx, false, "--git-dir", dir, "log", "-z", "--no-color", "--no-show-signature",
		"--format=%H%x00%an%x00%at%x00%s", "-n", strconv.Itoa(max+1), base+".."+head)
	if err != nil {
		return nil, false, err
	}
	fields := bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0})
	if len(out) == 0 {
		return nil, false, nil
	}
	if len(fields)%logFields != 0 {
		return nil, false, fmt.Errorf("%w: log output", ErrRawFormat)
	}
	n := len(fields) / logFields
	if n > max {
		return nil, true, nil
	}
	commits := make([]Commit, n)
	for i := range n {
		f := fields[i*logFields : (i+1)*logFields]
		at, err := strconv.ParseInt(string(f[2]), 10, 64)
		if err != nil || !isObjectName(string(f[0])) {
			return nil, false, fmt.Errorf("%w: log record", ErrRawFormat)
		}
		// git log lists newest first; revisions store oldest first.
		commits[n-1-i] = Commit{SHA: string(f[0]), AuthorName: string(f[1]), AuthoredAt: at, Subject: string(f[3])}
	}
	return commits, false, nil
}

// FileDiff is one changed file with its line counts.
type FileDiff struct {
	FileChange
	Added, Deleted int
	Binary         bool
}

// DiffSummary lists the files changed between commits from and to.
func (r *Runner) DiffSummary(ctx context.Context, dir, from, to string) ([]FileDiff, error) {
	if !isObjectName(from) || !isObjectName(to) {
		return nil, fmt.Errorf("%w: diff", ErrBadArgument)
	}
	rawOut, err := r.run(ctx, false, "--git-dir", dir, "diff", "--raw", "-z", "-M", "--no-abbrev", from, to)
	if err != nil {
		return nil, err
	}
	numOut, err := r.run(ctx, false, "--git-dir", dir, "diff", "--numstat", "-z", "-M", from, to)
	if err != nil {
		return nil, err
	}
	changes, err := ParseRaw(rawOut)
	if err != nil {
		return nil, err
	}
	counts, err := ParseNumstat(numOut)
	if err != nil {
		return nil, err
	}
	byPath := make(map[[2]string]LineCount, len(counts))
	for _, c := range counts {
		byPath[[2]string{c.OldPath, c.NewPath}] = c
	}
	diffs := make([]FileDiff, len(changes))
	for i, c := range changes {
		key := [2]string{c.OldPath, c.NewPath}
		switch c.Status {
		case StatusAdded:
			key = [2]string{c.NewPath, c.NewPath}
		case StatusDeleted:
			key = [2]string{c.OldPath, c.OldPath}
		}
		lc := byPath[key]
		diffs[i] = FileDiff{FileChange: c, Added: lc.Added, Deleted: lc.Deleted, Binary: lc.Binary}
	}
	return diffs, nil
}

// DiffFile returns the hunks of one file between commits from and to. For a
// rename, pass both paths so git can pair them; pass "" for the side that
// does not exist.
func (r *Runner) DiffFile(ctx context.Context, dir, from, to, oldPath, newPath string, limits Limits) ([]Hunk, error) {
	if !isObjectName(from) || !isObjectName(to) || (oldPath == "" && newPath == "") {
		return nil, fmt.Errorf("%w: diff", ErrBadArgument)
	}
	args := []string{"--git-dir", dir, "diff", "--no-color", "--no-ext-diff", "--no-textconv", "-U3", "-M", from, to, "--"}
	for _, p := range []string{oldPath, newPath} {
		if p != "" && (len(args) == 0 || args[len(args)-1] != p) {
			args = append(args, p)
		}
	}
	out, err := r.run(ctx, false, args...)
	if err != nil {
		return nil, err
	}
	return ParseHunks(out, limits)
}

// FileAt returns the content of path at commit, refusing files over max
// bytes with ErrTooLarge.
func (r *Runner) FileAt(ctx context.Context, dir, commit, path string, max int64) ([]byte, error) {
	if !isObjectName(commit) || path == "" {
		return nil, fmt.Errorf("%w: cat-file", ErrBadArgument)
	}
	sizeOut, err := r.run(ctx, false, "--git-dir", dir, "cat-file", "-s", commit+":"+path)
	if err != nil {
		return nil, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(sizeOut)), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: cat-file size", ErrRawFormat)
	}
	if size > max {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, size)
	}
	return r.run(ctx, false, "--git-dir", dir, "cat-file", "blob", commit+":"+path)
}

// Bundle writes every ref of the repository at dir, hidden ones included,
// to a bundle file at out.
func (r *Runner) Bundle(ctx context.Context, dir, out string) error {
	_, err := r.run(ctx, true, "--git-dir", dir, "bundle", "create", "--quiet", out, "--all")
	return err
}

func isObjectName(s string) bool {
	if len(s) != shaLen1 && len(s) != shaLen256 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// validInternalRef accepts refs/garret/... refs that follow the same rules
// as branch refs.
func validInternalRef(ref string) bool {
	rest, ok := strings.CutPrefix(ref, internalRefPrefix)
	return ok && ValidBranchRef(branchPrefix+rest) == nil
}

// ResolveRef returns the commit ref points at, or "" when ref does not
// exist. ref must be a branch ref or under refs/garret/.
func (r *Runner) ResolveRef(ctx context.Context, dir, ref string) (string, error) {
	if ValidBranchRef(ref) != nil && !validInternalRef(ref) {
		return "", fmt.Errorf("%w: rev-parse", ErrBadArgument)
	}
	out, err := r.run(ctx, false, "--git-dir", dir, "for-each-ref", "--format=%(objectname)", ref)
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(out))
	if sha != "" && !isObjectName(sha) {
		return "", fmt.Errorf("%w: for-each-ref output", ErrRawFormat)
	}
	return sha, nil
}
