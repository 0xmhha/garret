package enroll

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/garret/internal/pki"
)

// recorder is a Runner that records commands and fails the ones listed.
type recorder struct {
	ran  []string
	fail map[string]bool
}

func (r *recorder) Run(_ context.Context, name string, args ...string) error {
	line := strings.Join(append([]string{name}, args...), " ")
	r.ran = append(r.ran, line)
	if r.fail[name] {
		return errors.New("exit status 1")
	}
	return nil
}

func testCert(t *testing.T) *x509.Certificate {
	t.Helper()
	_, ca := testServer(t, enrollHandler)
	key, _ := NewKey()
	return signFor(t, ca, key)
}

// signFor issues a client certificate for key from ca.
func signFor(t *testing.T, ca *pki.CA, key *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	s, _ := pki.NewSerial(rand.Reader)
	tmpl, _ := pki.ClientTemplate(s, time.Now(), "01JD000000000000000000BOB0")
	c, err := pki.Sign(tmpl, ca.Cert, &key.PublicKey, ca.Key, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func commands(steps []Step) []string {
	var out []string
	for _, s := range steps {
		if s.Command != nil {
			out = append(out, strings.Join(s.Command, " "))
		}
	}
	return out
}

func runAll(t *testing.T, steps []Step) *recorder {
	t.Helper()
	r := &recorder{}
	if err := Apply(context.Background(), steps, r, &bytes.Buffer{}, false); err != nil {
		t.Fatal(err)
	}
	return r
}

const testURL = "https://review.local:8443"

func TestInstallStepsDarwin(t *testing.T) {
	home := t.TempDir()
	p := Platform{GOOS: "darwin", Home: home, Executable: "/opt/a&b/garret", UID: 501}
	files := FilesIn(ClientDir(p.GOOS, home, "", "review.local", 8443))
	cert := testCert(t)
	steps, err := InstallSteps(p, files, testURL, cert)
	if err != nil {
		t.Fatal(err)
	}
	kc := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
	plist := filepath.Join(home, "Library", "LaunchAgents", "io.garret.renew.review.local-8443.plist")
	want := []string{
		"git config --global http." + testURL + "/.sslCert " + files.Cert,
		"git config --global http." + testURL + "/.sslKey " + files.Key,
		"git config --global http." + testURL + "/.sslCAInfo " + files.CA,
		"security import " + files.Key + " -k " + kc + " -t priv -f openssl",
		"security import " + files.Cert + " -k " + kc + " -t cert",
		"security add-trusted-cert -r trustRoot -p ssl -s review.local -k " + kc + " " + files.CA,
		"security set-identity-preference -Z " + certSHA1(cert) + " -s " + testURL + "/ " + kc,
		"launchctl bootout gui/501/io.garret.renew.review.local-8443",
		"launchctl bootstrap gui/501 " + plist,
	}
	if got := commands(steps); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	r := runAll(t, steps)
	if len(r.ran) != len(want) {
		t.Fatalf("ran %d commands", len(r.ran))
	}
	data, err := os.ReadFile(plist)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"<string>/opt/a&amp;b/garret</string>", "<string>--server</string>", "<string>" + testURL + "</string>",
		"<integer>21600</integer>", "<key>RunAtLoad</key>", files.Dir + ".renew.log"} {
		if !strings.Contains(string(data), s) {
			t.Errorf("plist lacks %q:\n%s", s, data)
		}
	}
	if fi, _ := os.Stat(plist); fi.Mode().Perm() != 0o600 {
		t.Errorf("plist mode %v", fi.Mode())
	}
}

func TestInstallStepsLinux(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	p := Platform{GOOS: "linux", Home: home, XDGConfig: xdg, Executable: "/opt/my app/garret%", UID: 1000}
	files := FilesIn(ClientDir(p.GOOS, home, xdg, "review.local", 8443))
	steps, err := InstallSteps(p, files, testURL, testCert(t))
	if err != nil {
		t.Fatal(err)
	}
	got := commands(steps)
	tail := []string{"systemctl --user daemon-reload", "systemctl --user enable --now garret-renew-review.local-8443.timer"}
	if len(got) != 5 || strings.Join(got[3:], "\n") != strings.Join(tail, "\n") {
		t.Fatalf("commands %q", got)
	}
	runAll(t, steps)
	units := filepath.Join(xdg, "systemd", "user")
	svc, err := os.ReadFile(filepath.Join(units, "garret-renew-review.local-8443.service"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(svc), `ExecStart="/opt/my app/garret%%" "renew" "--server" "`+testURL+`"`) || !strings.Contains(string(svc), "Type=oneshot") {
		t.Fatalf("service:\n%s", svc)
	}
	timer, _ := os.ReadFile(filepath.Join(units, "garret-renew-review.local-8443.timer"))
	for _, s := range []string{"OnBootSec=5min", "OnUnitActiveSec=6h", "WantedBy=timers.target"} {
		if !strings.Contains(string(timer), s) {
			t.Errorf("timer lacks %q:\n%s", s, timer)
		}
	}
}

func TestInstallStepsOtherSystemsConfigureGitOnly(t *testing.T) {
	files := FilesIn(t.TempDir())
	steps, err := InstallSteps(Platform{GOOS: "freebsd", Home: t.TempDir()}, files, testURL, testCert(t))
	if err != nil || len(steps) != 3 || len(commands(steps)) != 3 {
		t.Fatalf("steps %d, %v", len(steps), err)
	}
}

func TestInstallStepsRefuseUnsafeHost(t *testing.T) {
	for _, u := range []string{"https://a_b:8443", "https://review.local", "http://review.local:8443", "not a url"} {
		if _, err := InstallSteps(Platform{GOOS: "darwin", Home: t.TempDir()}, FilesIn(t.TempDir()), u, testCert(t)); err == nil {
			t.Errorf("InstallSteps accepted %q", u)
		}
	}
}

func TestRenewSteps(t *testing.T) {
	home := t.TempDir()
	files := FilesIn(t.TempDir())
	oldCert, newCert := testCert(t), testCert(t)
	steps, err := RenewSteps(Platform{GOOS: "darwin", Home: home}, files, testURL, newCert, oldCert)
	if err != nil {
		t.Fatal(err)
	}
	kc := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
	got := commands(steps)
	if len(got) != 4 || got[2] != "security set-identity-preference -Z "+certSHA1(newCert)+" -s "+testURL+"/ "+kc ||
		got[3] != "security delete-identity -Z "+certSHA1(oldCert)+" "+kc || !steps[3].Optional {
		t.Fatalf("darwin renew %q", got)
	}
	if steps, err := RenewSteps(Platform{GOOS: "linux", Home: home}, files, testURL, newCert, oldCert); err != nil || len(steps) != 0 {
		t.Fatalf("linux renew %d %v", len(steps), err)
	}
}

func TestApply(t *testing.T) {
	var did bool
	steps := []Step{
		{Summary: "write a file", Do: func(context.Context) error { did = true; return nil }},
		{Summary: "remove an old job", Command: []string{"launchctl", "bootout", "x"}, Optional: true},
		{Summary: "configure git", Command: []string{"git", "config", "x"}},
		{Summary: "never reached", Command: []string{"true"}},
	}
	var out bytes.Buffer
	r := &recorder{}
	if err := Apply(context.Background(), steps, r, &out, true); err != nil || did || len(r.ran) != 0 || !strings.Contains(out.String(), "never reached") {
		t.Fatalf("dry run: err %v did %v ran %q out %s", err, did, r.ran, out.String())
	}
	out.Reset()
	r = &recorder{fail: map[string]bool{"launchctl": true, "git": true}}
	err := Apply(context.Background(), steps, r, &out, false)
	if err == nil || !strings.Contains(err.Error(), "configure git") || !did || len(r.ran) != 2 {
		t.Fatalf("real run: err %v did %v ran %q", err, did, r.ran)
	}
	if !strings.Contains(out.String(), "remove an old job") {
		t.Fatalf("output %s", out.String())
	}
}

func TestExecRunner(t *testing.T) {
	r := ExecRunner{Timeout: 200 * time.Millisecond}
	if err := r.Run(context.Background(), "true"); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), "false"); err == nil {
		t.Fatal("false succeeded")
	}
	start := time.Now()
	if err := r.Run(context.Background(), "sleep", "5"); err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("timeout not applied: %v after %v", err, time.Since(start))
	}
}

func TestInteractiveStepsGetLongerTimeout(t *testing.T) {
	steps, err := InstallSteps(Platform{GOOS: "darwin", Home: t.TempDir()}, FilesIn(t.TempDir()), testURL, testCert(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		long := len(s.Command) > 1 && s.Command[0] == "security" && (s.Command[1] == "add-trusted-cert" || s.Command[1] == "import")
		if long != (s.Timeout == interactiveTimeout) {
			t.Errorf("%q timeout %v", s.Summary, s.Timeout)
		}
	}
}

// deadlineRunner records whether the context had a deadline.
type deadlineRunner struct{ left time.Duration }

func (d *deadlineRunner) Run(ctx context.Context, _ string, _ ...string) error {
	if dl, ok := ctx.Deadline(); ok {
		d.left = time.Until(dl)
	}
	return nil
}

func TestApplyPassesStepTimeout(t *testing.T) {
	d := &deadlineRunner{}
	_ = Apply(context.Background(), []Step{{Summary: "s", Command: []string{"x"}, Timeout: time.Hour}}, d, &bytes.Buffer{}, false)
	if d.left < 59*time.Minute {
		t.Fatalf("deadline in %v", d.left)
	}
}

func TestInstallStepsWithoutCertificateUsePlaceholder(t *testing.T) {
	steps, err := InstallSteps(Platform{GOOS: "darwin", Home: t.TempDir()}, FilesIn(t.TempDir()), testURL, nil)
	if err != nil || !strings.Contains(strings.Join(commands(steps), "\n"), "-Z "+certPlaceholder+" ") {
		t.Fatalf("dry-run steps %q %v", commands(steps), err)
	}
}

func TestSaveReplacesAllFilesTogether(t *testing.T) {
	_, ca := testServer(t, enrollHandler)
	dir := filepath.Join(t.TempDir(), "client", "review.local-8443")
	files := FilesIn(dir)
	k1, _ := NewKey()
	c1 := signFor(t, ca, k1)
	if err := files.Save(k1, c1, ca.Cert); err != nil {
		t.Fatal(err)
	}
	// A failed save leaves the previous set in place.
	if err := os.WriteFile(dir+stagingSuffix, nil, 0o600); err != nil { // a file where the staging directory goes
		t.Fatal(err)
	}
	k2, _ := NewKey()
	c2 := signFor(t, ca, k2)
	if err := files.Save(k2, c2, ca.Cert); err == nil {
		t.Fatal("save over a blocked staging path succeeded")
	}
	if k, c, err := files.Load(); err != nil || !k.Equal(k1) || !c.Equal(c1) {
		t.Fatalf("previous set lost: %v", err)
	}
	_ = os.Remove(dir + stagingSuffix)
	// Interrupted after the old set moved aside and before the new one
	// moved in: Load finishes the swap.
	if err := files.Save(k2, c2, ca.Cert); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, dir+stagingSuffix); err != nil {
		t.Fatal(err)
	}
	if k, c, err := files.Load(); err != nil || !k.Equal(k2) || !c.Equal(c2) {
		t.Fatalf("interrupted swap not recovered: %v", err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("directory after recovery: %v", err)
	}
}
