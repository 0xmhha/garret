package gitrepo

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Service is a Git smart HTTP service.
type Service string

// The two services of the smart HTTP protocol.
const (
	ServiceUploadPack  Service = "git-upload-pack"
	ServiceReceivePack Service = "git-receive-pack"
)

// protocolPattern accepts the Git-Protocol header values Git sends, for
// example "version=2"; anything else is refused before it reaches git.
var protocolPattern = regexp.MustCompile(`^[a-z0-9]+=[A-Za-z0-9._-]+(:[a-z0-9]+=[A-Za-z0-9._-]+)*$`)

// Serve runs one smart HTTP exchange for the repository at dir: with
// advertise, the ref advertisement; otherwise the request read from stdin,
// with the result written to stdout. It runs under the semaphore and the
// long timeout, and is killed when ctx is done. protocol is the client's
// Git-Protocol header, passed to git as GIT_PROTOCOL.
func (r *Runner) Serve(ctx context.Context, svc Service, dir string, advertise bool, protocol string, stdin io.Reader, stdout io.Writer) error {
	if svc != ServiceUploadPack && svc != ServiceReceivePack {
		return fmt.Errorf("%w: service %q", ErrBadArgument, svc)
	}
	if protocol != "" && !protocolPattern.MatchString(protocol) {
		return fmt.Errorf("%w: Git-Protocol header", ErrBadArgument)
	}
	if err := r.sem.Acquire(ctx, 1); err != nil {
		return err
	}
	defer r.sem.Release(1)
	ctx, cancel := context.WithTimeout(ctx, r.longTimeout)
	defer cancel()

	args := []string{strings.TrimPrefix(string(svc), "git-"), "--stateless-rpc"}
	if advertise {
		args = append(args, "--advertise-refs")
	}
	args = append(args, "--", dir)
	stderr := &cappedBuffer{limit: maxStderr, truncate: true}
	cmd := exec.CommandContext(ctx, r.git, args...) // #nosec G204 -- fixed subcommands, validated directory
	cmd.Env = r.env
	if protocol != "" {
		cmd.Env = append(append([]string(nil), r.env...), "GIT_PROTOCOL="+protocol)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("gitrepo: %s: %w", svc, ctx.Err())
		}
		return fmt.Errorf("gitrepo: %s: %w: %s", svc, err, strings.TrimSpace(stderr.buf.String()))
	}
	return nil
}
