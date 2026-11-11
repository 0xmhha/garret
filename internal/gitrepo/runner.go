package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/semaphore"
)

var (
	// ErrGitVersion is returned when the git binary is older than 2.38.
	ErrGitVersion = errors.New("gitrepo: git 2.38 or later is required")
	// ErrBadArgument is returned before running git when a commit name or
	// ref does not have the expected form.
	ErrBadArgument = errors.New("gitrepo: invalid argument")
)

// Limits from docs/specs/components.md section 8.
const (
	DefaultTimeout     = 10 * time.Second // Q3, most commands
	DefaultLongTimeout = 60 * time.Second // Q3, bundle and transfers
	DefaultOutputCap   = 64 << 20         // Q4
	maxStderr          = 4 << 10
	minGitMajor        = 2
	minGitMinor        = 38
	internalRefPrefix  = "refs/garret/"
	// systemPath is searched by git for its helper programs.
	systemPath = "/usr/bin:/bin:/usr/sbin:/sbin"
)

// Runner runs git commands with a bounded number running at once, a
// timeout on each, a minimal environment and a cap on captured output. It
// is safe for concurrent use.
type Runner struct {
	git         string
	env         []string
	sem         *semaphore.Weighted
	timeout     time.Duration
	longTimeout time.Duration
	outputCap   int64
}

// RunnerOption configures NewRunner.
type RunnerOption func(*Runner)

// WithTimeouts replaces the default command timeouts.
func WithTimeouts(normal, long time.Duration) RunnerOption {
	return func(r *Runner) { r.timeout, r.longTimeout = normal, long }
}

// WithOutputCap replaces the default cap on captured output.
func WithOutputCap(n int64) RunnerOption {
	return func(r *Runner) { r.outputCap = n }
}

// NewRunner checks that gitPath is git 2.38 or later and returns a Runner
// that allows limit commands at once. home becomes HOME for every command.
func NewRunner(ctx context.Context, gitPath string, limit int, home string, opts ...RunnerOption) (*Runner, error) {
	if limit < 1 {
		return nil, fmt.Errorf("%w: limit %d", ErrBadArgument, limit)
	}
	r := &Runner{
		git:         gitPath,
		sem:         semaphore.NewWeighted(int64(limit)),
		timeout:     DefaultTimeout,
		longTimeout: DefaultLongTimeout,
		outputCap:   DefaultOutputCap,
		env: []string{
			"PATH=" + filepath.Dir(gitPath) + ":" + systemPath,
			"HOME=" + home,
			"LANG=C",
			"LC_ALL=C",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_TERMINAL_PROMPT=0",
		},
	}
	for _, o := range opts {
		o(r)
	}
	// The version check uses the defaults, not the configured limits.
	out, err := r.exec(ctx, DefaultTimeout, DefaultOutputCap, "--version")
	if err != nil {
		return nil, err
	}
	if !versionSupported(string(out)) {
		return nil, fmt.Errorf("%w: found %q", ErrGitVersion, strings.TrimSpace(string(out)))
	}
	return r, nil
}

var versionPattern = regexp.MustCompile(`^git version (\d+)\.(\d+)`)

func versionSupported(s string) bool {
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return false
	}
	major, err1 := strconv.Atoi(m[1])
	minor, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil {
		return false
	}
	return major > minGitMajor || major == minGitMajor && minor >= minGitMinor
}

// run executes git with args. long selects the long timeout. The error
// names the subcommand and includes trimmed stderr, never the arguments.
func (r *Runner) run(ctx context.Context, long bool, args ...string) ([]byte, error) {
	timeout := r.timeout
	if long {
		timeout = r.longTimeout
	}
	return r.exec(ctx, timeout, r.outputCap, args...)
}

func (r *Runner) exec(ctx context.Context, timeout time.Duration, outputCap int64, args ...string) ([]byte, error) {
	if err := r.sem.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	defer r.sem.Release(1)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdout := &cappedBuffer{limit: outputCap}
	stderr := &cappedBuffer{limit: maxStderr, truncate: true}
	cmd := exec.CommandContext(ctx, r.git, args...) // #nosec G204 -- no shell; arguments are validated by callers
	cmd.Env = r.env
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	sub := subcommand(args)
	switch {
	case stdout.over:
		return nil, fmt.Errorf("%w: git %s output over %d bytes", ErrTooLarge, sub, outputCap)
	case ctx.Err() != nil:
		return nil, fmt.Errorf("gitrepo: git %s: %w", sub, ctx.Err())
	case err != nil:
		return nil, fmt.Errorf("gitrepo: git %s: %w: %s", sub, err, strings.TrimSpace(stderr.buf.String()))
	}
	return stdout.buf.Bytes(), nil
}

// subcommand returns the first argument that is not a global option.
func subcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--git-dir" || a == "-c":
			i++
		case !strings.HasPrefix(a, "-"):
			return a
		}
	}
	return strings.Join(args, " ")
}

// cappedBuffer stores at most limit bytes. Beyond it, it either reports
// over and fails the write (which stops git), or silently truncates.
type cappedBuffer struct {
	buf      bytes.Buffer
	limit    int64
	truncate bool
	over     bool
}

var errCapped = errors.New("output cap reached")

func (b *cappedBuffer) Write(p []byte) (int, error) {
	room := b.limit - int64(b.buf.Len())
	if int64(len(p)) <= room {
		return b.buf.Write(p)
	}
	if room > 0 {
		b.buf.Write(p[:room])
	}
	if b.truncate {
		return len(p), nil
	}
	b.over = true
	return 0, errCapped
}
