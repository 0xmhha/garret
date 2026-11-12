package enroll

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // the keychain names certificates by SHA-1; no security decision rests on it
	"crypto/x509"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	renewInterval    = 6 * time.Hour
	defaultCmdLimit  = 60 * time.Second
	maxCommandOutput = 512
	goosDarwin       = "darwin"
	goosLinux        = "linux"
)

var safeHost = regexp.MustCompile(`^[A-Za-z0-9.:-]+$`)

// Platform describes the device the installation runs on. Tests pass a
// temporary home.
type Platform struct {
	GOOS       string
	Home       string
	XDGConfig  string // $XDG_CONFIG_HOME, empty when unset
	Executable string // the garret binary the renewal job runs
	UID        int
}

// Step is one installation step: either a command, run without a shell, or
// an in-process action.
type Step struct {
	Summary  string
	Command  []string
	Do       func(context.Context) error
	Optional bool // a failure is reported and the installation goes on
}

// Runner runs a command. ExecRunner is the real one.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
}

// ExecRunner runs commands with exec, each bounded by Timeout (60 seconds
// when zero).
type ExecRunner struct{ Timeout time.Duration }

// Run runs name with args and returns an error that includes the start of
// the command's output when it fails.
func (r ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	limit := r.Timeout
	if limit == 0 {
		limit = defaultCmdLimit
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- fixed programs; arguments are never passed to a shell
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) > maxCommandOutput {
			out = out[:maxCommandOutput]
		}
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Apply runs steps in order and writes each summary to w. With dryRun it
// only writes the summaries. It stops at the first failing step that is not
// optional.
func Apply(ctx context.Context, steps []Step, r Runner, w io.Writer, dryRun bool) error {
	for _, s := range steps {
		if dryRun {
			_, _ = fmt.Fprintf(w, "would: %s\n", s.Summary)
			continue
		}
		_, _ = fmt.Fprintf(w, "%s\n", s.Summary)
		var err error
		switch {
		case s.Do != nil:
			err = s.Do(ctx)
		case len(s.Command) > 0:
			err = r.Run(ctx, s.Command[0], s.Command[1:]...)
		}
		if err != nil && s.Optional {
			_, _ = fmt.Fprintf(w, "  skipped: %v\n", err)
			continue
		}
		if err != nil {
			return fmt.Errorf("enroll: %s: %w", s.Summary, err)
		}
	}
	return nil
}

// InstallSteps returns the steps that point Git, the keychain and the
// renewal job at the saved files (transport.md 6.5).
func InstallSteps(p Platform, f Files, serverURL string, cert *x509.Certificate) ([]Step, error) {
	name, err := jobName(serverURL)
	if err != nil {
		return nil, err
	}
	var steps []Step
	for _, args := range GitConfigArgs(serverURL, f) {
		steps = append(steps, Step{Summary: "git " + strings.Join(args[:3], " "), Command: append([]string{"git"}, args...)})
	}
	switch p.GOOS {
	case goosDarwin:
		steps = append(steps, importSteps(p, f)...)
		steps = append(steps,
			Step{Summary: "trust the garret CA for this user", Command: []string{"security", "add-trusted-cert", "-r", "trustRoot", "-p", "ssl", "-k", keychain(p), f.CA}},
			preferenceStep(p, serverURL, cert))
		steps = append(steps, launchdSteps(p, f, serverURL, name)...)
	case goosLinux:
		steps = append(steps, systemdSteps(p, serverURL, name)...)
	}
	return steps, nil
}

// RenewSteps returns the steps after a renewal saved new files. Only macOS
// needs any, because Git reads the files directly.
func RenewSteps(p Platform, f Files, serverURL string, newCert, oldCert *x509.Certificate) ([]Step, error) {
	if _, err := jobName(serverURL); err != nil {
		return nil, err
	}
	if p.GOOS != goosDarwin {
		return nil, nil
	}
	steps := append(importSteps(p, f), preferenceStep(p, serverURL, newCert))
	return append(steps, Step{Summary: "remove the previous certificate from the keychain", Optional: true,
		Command: []string{"security", "delete-identity", "-Z", certSHA1(oldCert), keychain(p)}}), nil
}

// importSteps put the key and certificate into the login keychain, where
// they pair into an identity.
func importSteps(p Platform, f Files) []Step {
	kc := keychain(p)
	return []Step{
		{Summary: "import the device key into the login keychain", Command: []string{"security", "import", f.Key, "-k", kc, "-t", "priv"}},
		{Summary: "import the certificate into the login keychain", Command: []string{"security", "import", f.Cert, "-k", kc, "-t", "cert"}},
	}
}

// preferenceStep makes the browsers present cert for serverURL without
// asking which certificate to use.
func preferenceStep(p Platform, serverURL string, cert *x509.Certificate) Step {
	return Step{Summary: "use the certificate for " + serverURL,
		Command: []string{"security", "set-identity-preference", "-Z", certSHA1(cert), "-s", serverURL + "/", keychain(p)}}
}

func launchdSteps(p Platform, f Files, serverURL, name string) []Step {
	label := "io.garret.renew." + name
	plist := filepath.Join(p.Home, "Library", "LaunchAgents", label+".plist")
	data := launchdPlist(label, p.Executable, serverURL, filepath.Join(f.Dir, "renew.log"))
	domain := "gui/" + strconv.Itoa(p.UID)
	return []Step{
		{Summary: "write the renewal job " + plist, Do: func(context.Context) error { return writeIn(plist, data) }},
		{Summary: "unload an earlier renewal job", Optional: true, Command: []string{"launchctl", "bootout", domain + "/" + label}},
		{Summary: "load the renewal job", Command: []string{"launchctl", "bootstrap", domain, plist}},
	}
}

func systemdSteps(p Platform, serverURL, name string) []Step {
	base := p.XDGConfig
	if base == "" {
		base = filepath.Join(p.Home, ".config")
	}
	units := filepath.Join(base, "systemd", "user")
	unit := "garret-renew-" + name
	service := "[Unit]\nDescription=Renew the garret client certificate for " + serverURL + "\n\n[Service]\nType=oneshot\nExecStart=" +
		systemdQuote(p.Executable, "renew", "--server", serverURL) + "\n"
	timer := "[Unit]\nDescription=Renew the garret client certificate every 6 hours\n\n[Timer]\nOnBootSec=5min\nOnUnitActiveSec=" +
		strconv.Itoa(int(renewInterval.Hours())) + "h\n\n[Install]\nWantedBy=timers.target\n"
	return []Step{
		{Summary: "write " + filepath.Join(units, unit+".service"), Do: func(context.Context) error {
			return writeIn(filepath.Join(units, unit+".service"), []byte(service))
		}},
		{Summary: "write " + filepath.Join(units, unit+".timer"), Do: func(context.Context) error {
			return writeIn(filepath.Join(units, unit+".timer"), []byte(timer))
		}},
		{Summary: "reload systemd user units", Command: []string{"systemctl", "--user", "daemon-reload"}},
		{Summary: "start the renewal timer", Command: []string{"systemctl", "--user", "enable", "--now", unit + ".timer"}},
	}
}

// jobName returns <host>-<port> for serverURL, refusing anything that is
// not a plain HTTPS origin with a host safe in file names and labels.
func jobName(serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("enroll: bad server URL %q", serverURL)
	}
	if err := checkServerURL(serverURL, u.Hostname()); err != nil {
		return "", err
	}
	if !safeHost.MatchString(u.Hostname()) {
		return "", errors.New("enroll: the server's host name has characters garret cannot use in file names")
	}
	return u.Hostname() + "-" + u.Port(), nil
}

func keychain(p Platform) string {
	return filepath.Join(p.Home, "Library", "Keychains", "login.keychain-db")
}

func certSHA1(c *x509.Certificate) string {
	sum := sha1.Sum(c.Raw) //nolint:gosec // see the import
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func launchdPlist(label, exe, serverURL, logPath string) []byte {
	esc := func(s string) string {
		var b bytes.Buffer
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + esc(label) + `</string>
	<key>ProgramArguments</key>
	<array>
`)
	for _, a := range []string{exe, "renew", "--server", serverURL} {
		b.WriteString("\t\t<string>" + esc(a) + "</string>\n")
	}
	b.WriteString(`	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>StartInterval</key>
	<integer>` + strconv.Itoa(int(renewInterval.Seconds())) + `</integer>
	<key>StandardOutPath</key>
	<string>` + esc(logPath) + `</string>
	<key>StandardErrorPath</key>
	<string>` + esc(logPath) + `</string>
</dict>
</plist>
`)
	return []byte(b.String())
}

// systemdQuote quotes each argument for ExecStart: double quotes, with
// backslash and quote escaped, and % and $ doubled so systemd does not
// expand them.
func systemdQuote(args ...string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`)
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = `"` + r.Replace(a) + `"`
	}
	return strings.Join(quoted, " ")
}

// writeIn creates path's directory (0700 when new) and writes data with
// mode 0600.
func writeIn(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeFile(path, data)
}
