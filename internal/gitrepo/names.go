package gitrepo

import (
	"errors"
	"regexp"
	"strings"
)

var (
	// ErrProjectName is returned for a project name outside the allowed form.
	ErrProjectName = errors.New("gitrepo: invalid project name")
	// ErrBranchRef is returned for a ref that is not a valid branch ref.
	ErrBranchRef = errors.New("gitrepo: invalid branch ref")
)

const (
	branchPrefix = "refs/heads/"
	maxRefBytes  = 255
)

var projectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidProjectName returns ErrProjectName unless s matches
// ^[a-z0-9][a-z0-9._-]{0,63}$, contains no "..", and does not end in ".git"
// or ".lock". The name becomes a directory under data/repos.
func ValidProjectName(s string) error {
	if !projectPattern.MatchString(s) || strings.Contains(s, "..") ||
		strings.HasSuffix(s, ".git") || strings.HasSuffix(s, ".lock") {
		return ErrProjectName
	}
	return nil
}

// ValidBranchRef returns ErrBranchRef unless s is a full branch ref
// (refs/heads/...) of at most 255 bytes that git check-ref-format accepts.
func ValidBranchRef(s string) error {
	if len(s) > maxRefBytes || !strings.HasPrefix(s, branchPrefix) || len(s) == len(branchPrefix) {
		return ErrBranchRef
	}
	if strings.Contains(s, "..") || strings.Contains(s, "@{") || strings.HasSuffix(s, ".") {
		return ErrBranchRef
	}
	for i := range len(s) {
		if forbiddenRefByte(s[i]) {
			return ErrBranchRef
		}
	}
	for _, c := range strings.Split(s, "/") {
		if c == "" || strings.HasPrefix(c, ".") || strings.HasSuffix(c, ".lock") {
			return ErrBranchRef
		}
	}
	return nil
}

func forbiddenRefByte(b byte) bool {
	const del = 0x7f
	if b < ' ' || b == del {
		return true
	}
	return strings.IndexByte(" ~^:?*[\\", b) >= 0
}
