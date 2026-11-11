package gitrepo

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestValidProjectName(t *testing.T) {
	tests := []struct {
		in string
		ok bool
	}{
		{"garret", true},
		{"a", true},
		{"0", true},
		{"my-project_2.x", true},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), false},
		{"", false},
		{"Garret", false},
		{".hidden", false},
		{"-dash", false},
		{"a..b", false},
		{"repo.git", false},
		{"repo.lock", false},
		{"a/b", false},
		{"a b", false},
		{"한글", false},
		{"abc\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			err := ValidProjectName(tt.in)
			if tt.ok != (err == nil) {
				t.Fatalf("ValidProjectName(%q) = %v, want ok=%v", tt.in, err, tt.ok)
			}
			if err != nil && !errors.Is(err, ErrProjectName) {
				t.Fatalf("error %v is not ErrProjectName", err)
			}
		})
	}
}

// branchCases follows the rules and examples of git-check-ref-format(1).
var branchCases = []struct {
	in string
	ok bool
}{
	{"refs/heads/main", true},
	{"refs/heads/feature/login", true},
	{"refs/heads/fix-1.2", true},
	{"refs/heads/a.b", true},
	{"refs/heads/@", true},
	{"refs/heads/한글", true},
	{"refs/heads/" + strings.Repeat("a", 244), true},
	{"refs/heads/" + strings.Repeat("a", 245), false},
	{"main", false},
	{"refs/tags/v1", false},
	{"refs/heads/", false},
	{"refs/heads/.hidden", false},
	{"refs/heads/a/.b", false},
	{"refs/heads/a.lock", false},
	{"refs/heads/a.lock/b", false},
	{"refs/heads/a..b", false},
	{"refs/heads/a b", false},
	{"refs/heads/a\tb", false},
	{"refs/heads/a\x7fb", false},
	{"refs/heads/a~1", false},
	{"refs/heads/a^", false},
	{"refs/heads/a:b", false},
	{"refs/heads/a?", false},
	{"refs/heads/a*", false},
	{"refs/heads/a[b", false},
	{"refs/heads/a/", false},
	{"refs/heads//a", false},
	{"refs/heads/a//b", false},
	{"refs/heads/a.", false},
	{"refs/heads/a@{1}", false},
	{"refs/heads/a\\b", false},
}

func TestValidBranchRef(t *testing.T) {
	for _, tt := range branchCases {
		t.Run(tt.in, func(t *testing.T) {
			err := ValidBranchRef(tt.in)
			if tt.ok != (err == nil) {
				t.Fatalf("ValidBranchRef(%q) = %v, want ok=%v", tt.in, err, tt.ok)
			}
			if err != nil && !errors.Is(err, ErrBranchRef) {
				t.Fatalf("error %v is not ErrBranchRef", err)
			}
		})
	}
}

// TestValidBranchRefMatchesGit checks every case against the real
// git check-ref-format, except the length limit, which is garret's own.
func TestValidBranchRefMatchesGit(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	for _, tt := range branchCases {
		if len(tt.in) > maxRefBytes || !strings.HasPrefix(tt.in, branchPrefix) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		gitOK := exec.CommandContext(ctx, gitPath, "check-ref-format", tt.in).Run() == nil
		cancel()
		if gitOK != (ValidBranchRef(tt.in) == nil) {
			t.Errorf("%q: git says ok=%v, ValidBranchRef disagrees", tt.in, gitOK)
		}
	}
}

func FuzzValidBranchRef(f *testing.F) {
	for _, tt := range branchCases {
		f.Add(tt.in)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if ValidBranchRef(s) != nil {
			return
		}
		// Accepted refs never contain sequences git forbids.
		for _, bad := range []string{"..", "//", "@{", "\\", " ", "~", "^", ":", "?", "*", "["} {
			if strings.Contains(s, bad) {
				t.Fatalf("accepted %q containing %q", s, bad)
			}
		}
	})
}
