package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xmhha/garret/internal/app"
	"github.com/0xmhha/garret/internal/config"
	"github.com/0xmhha/garret/internal/enroll"
	"github.com/0xmhha/garret/internal/netguard"
)

const testPassword = "correct horse battery staple"

// shifted is the wall clock moved forward by a settable offset; the test
// server and the commands share one.
type shifted struct{ off atomic.Int64 }

func (s *shifted) Now() time.Time          { return time.Now().Add(time.Duration(s.off.Load())) }
func (s *shifted) advance(d time.Duration) { s.off.Add(int64(d)) }

var testClock = &shifted{}

// answers is a Prompter that replies in order and records the labels.
type answers struct {
	replies []string
	asked   []string
}

func (a *answers) next(label string) (string, error) {
	a.asked = append(a.asked, label)
	if len(a.replies) == 0 {
		return "", errors.New("no answer for " + label)
	}
	r := a.replies[0]
	a.replies = a.replies[1:]
	return r, nil
}

func (a *answers) Ask(label, def string) (string, error) {
	r, err := a.next(label)
	if r == "" {
		return def, err
	}
	return r, err
}
func (a *answers) Secret(label string) (string, error) { return a.next(label) }

// recorder is an enroll.Runner that records commands.
type recorder struct {
	mu  sync.Mutex
	ran []string
}

func (r *recorder) Run(_ context.Context, name string, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ran = append(r.ran, name+" "+strings.Join(args, " "))
	return nil
}

func freePort(t *testing.T) uint16 {
	t.Helper()
	l, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return uint16(l.Addr().(*net.TCPAddr).Port)
}

// testServer is a running garret server with client certificates on.
type testServer struct {
	app        *app.App
	dataDir    string
	mainPort   uint16
	enrollPort uint16
	out        *bytes.Buffer
	stop       func() error
}

// startServer runs a server on dataDir (a new one when empty) until stop
// or the end of the test, with client certificates required. Zero ports are
// chosen; a restart passes the previous ones. listen, when set, opens the
// sockets.
func startServer(t *testing.T, dataDir string, mainPort, enrollPort uint16, listen func(context.Context, string) (net.Listener, error)) *testServer {
	t.Helper()
	return startServerCerts(t, dataDir, mainPort, enrollPort, listen, true)
}

// startServerCerts is startServer with client certificates on or off
// (serve --client-cert).
func startServerCerts(t *testing.T, dataDir string, mainPort, enrollPort uint16, listen func(context.Context, string) (net.Listener, error), clientCerts bool) *testServer {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	if dataDir == "" {
		dataDir = filepath.Join(t.TempDir(), "data")
	}
	if mainPort == 0 {
		mainPort = freePort(t)
	}
	if enrollPort == 0 {
		enrollPort = freePort(t)
	}
	cfg := config.Serve{DataDir: dataDir, Host: "localhost",
		Listen:       netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), mainPort),
		EnrollListen: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), enrollPort),
		Port:         mainPort, EnrollPort: enrollPort, ClientCerts: clientCerts}
	out := &bytes.Buffer{}
	sys := app.System{Clock: testClock, Rand: rand.Reader, GitPath: git, Log: quiet(), Out: out, Listen: listen,
		ActiveRefresh: 50 * time.Millisecond,
		Interfaces: func() ([]netguard.Interface, error) {
			return []netguard.Interface{{Name: "en0", Up: true, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.20/24")}}}, nil
		}}
	a, err := app.New(context.Background(), cfg, sys)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	stop := sync.OnceValue(func() error { cancel(); return <-done })
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Errorf("server: %v", err)
		}
	})
	return &testServer{app: a, dataDir: dataDir, mainPort: mainPort, enrollPort: enrollPort, out: out, stop: stop}
}

// server runs a new server until the test ends.
func server(t *testing.T) (*app.App, string) {
	t.Helper()
	s := startServer(t, "", 0, 0, nil)
	return s.app, s.dataDir
}

func depsFor(t *testing.T, home string, replies ...string) (deps, *answers, *recorder, *bytes.Buffer) {
	t.Helper()
	ans := &answers{replies: replies}
	rec := &recorder{}
	var out bytes.Buffer
	return deps{stdout: &out, stderr: &out, prompt: ans, runner: rec, clock: testClock, hostname: "test-machine",
		platform: enroll.Platform{GOOS: "freebsd", Home: home, Executable: "/usr/local/bin/garret"}}, ans, rec, &out
}

func mainPort(t *testing.T, a *app.App) string {
	t.Helper()
	u, err := url.Parse(a.MainURL())
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}

func clientDir(home, port string) string {
	p, _ := strconv.Atoi(port)
	return enroll.ClientDir("freebsd", home, "", "localhost", uint16(p))
}

// session logs in with the certificate in dir and returns a client, the
// cookie and the CSRF token.
func session(t *testing.T, a *app.App, dir, username string) (*http.Client, *http.Cookie, string) {
	t.Helper()
	files := enroll.FilesIn(dir)
	key, cert, err := files.Load()
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(a.CA())
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS13,
		Certificates: []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key, Leaf: cert}}}}}
	body, _ := json.Marshal(map[string]string{"username": username, "password": testPassword})
	var resp *http.Response
	// The server loads new certificates within its refresh interval.
	for range 100 {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, a.MainURL()+"/api/v1/auth/login", bytes.NewReader(body))
		req.Header.Set("Origin", a.MainURL())
		req.Header.Set("Content-Type", "application/json")
		if resp, err = c.Do(req); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		CSRF string `json:"csrf_token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK || len(resp.Cookies()) == 0 {
		t.Fatalf("login status %d", resp.StatusCode)
	}
	return c, resp.Cookies()[0], out.CSRF
}

func invite(t *testing.T, a *app.App, c *http.Client, cookie *http.Cookie, csrf string) (link, code string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, a.MainURL()+"/api/v1/admin/invitations",
		strings.NewReader(`{"role":"member"}`))
	req.Header.Set("Origin", a.MainURL())
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Link string `json:"link"`
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite %d %v", resp.StatusCode, err)
	}
	waitOpen(t, out.Link)
	return out.Link, out.Code
}

// waitOpen waits until the enrollment listener in link accepts: the gate
// opens it shortly after the invitation is created.
func waitOpen(t *testing.T, link string) {
	t.Helper()
	l, err := enroll.ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(int(l.Port)))
	for range 200 {
		c, err := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "tcp", addr)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("enrollment listener did not open")
}

func TestSetupEnrollRenew(t *testing.T) {
	a, dataDir := server(t)
	port := mainPort(t, a)

	adminHome := t.TempDir()
	d, _, rec, out := depsFor(t, adminHome, "root", "Root", testPassword, testPassword, "host")
	if code := runWith([]string{"admin", "setup", "--data", dataDir, "--host", "localhost", "--port", port}, d); code != exitOK {
		t.Fatalf("admin setup exit %d:\n%s", code, out)
	}
	if len(rec.ran) != 3 || !strings.HasPrefix(rec.ran[0], "git config --global http."+a.MainURL()+"/.sslCert") {
		t.Fatalf("setup commands %q", rec.ran)
	}
	d, _, _, out = depsFor(t, t.TempDir(), "root2", "Root", testPassword, testPassword, "host")
	if code := runWith([]string{"admin", "setup", "--data", dataDir, "--host", "localhost", "--port", port}, d); code == exitOK ||
		!strings.Contains(out.String(), "administrator already exists") {
		t.Fatalf("second setup exit %d:\n%s", code, out)
	}

	c, cookie, csrf := session(t, a, clientDir(adminHome, port), "root")
	link, inviteCode := invite(t, a, c, cookie, csrf)

	memberHome := t.TempDir()
	d, _, _, out = depsFor(t, memberHome)
	if code := runWith([]string{"enroll", "--dry-run", link}, d); code != exitOK || !strings.Contains(out.String(), "would: ") {
		t.Fatalf("dry run exit %d:\n%s", code, out)
	}
	if _, err := os.Stat(clientDir(memberHome, port)); err == nil {
		t.Fatal("dry run wrote files")
	}
	d, _, _, out = depsFor(t, memberHome, inviteCode, "bob", "Bob", testPassword, "another password here", "laptop")
	if code := runWith([]string{"enroll", link}, d); code == exitOK || !strings.Contains(out.String(), "do not match") {
		t.Fatalf("mismatched passwords exit %d:\n%s", code, out)
	}
	d, ans, rec, out := depsFor(t, memberHome, inviteCode, "bob", "Bob", testPassword, testPassword, "")
	if code := runWith([]string{"enroll", link}, d); code != exitOK {
		t.Fatalf("enroll exit %d:\n%s\nasked %q", code, out, ans.asked)
	}
	if len(rec.ran) != 3 {
		t.Fatalf("enroll commands %q", rec.ran)
	}
	files := enroll.FilesIn(clientDir(memberHome, port))
	_, before, err := files.Load()
	if err != nil {
		t.Fatal(err)
	}
	session(t, a, files.Dir, "bob")

	// Leftovers of an interrupted save are not servers.
	_ = os.MkdirAll(files.Dir+".old", 0o700)
	d, _, _, out = depsFor(t, memberHome)
	if code := runWith([]string{"renew"}, d); code != exitOK || !strings.Contains(out.String(), "not due") || strings.Contains(out.String(), ".old") {
		t.Fatalf("renew when not due exit %d:\n%s", code, out)
	}
	testClock.advance(4 * 24 * time.Hour) // into the second half of the validity
	t.Cleanup(func() { testClock.advance(-4 * 24 * time.Hour) })
	d, _, _, out = depsFor(t, memberHome)
	if code := runWith([]string{"renew", "--server", a.MainURL()}, d); code != exitOK {
		t.Fatalf("renew exit %d:\n%s", code, out)
	}
	_, after, err := files.Load()
	if err != nil || after.SerialNumber.Cmp(before.SerialNumber) == 0 {
		t.Fatalf("certificate not replaced: %v", err)
	}
	session(t, a, files.Dir, "bob")
	// The new certificate is in its first half: nothing to do.
	d, _, _, out = depsFor(t, memberHome)
	if code := runWith([]string{"renew"}, d); code != exitOK || !strings.Contains(out.String(), "not due") {
		t.Fatalf("renew right after renewal exit %d:\n%s", code, out)
	}
}

func TestSetupWithoutCA(t *testing.T) {
	d, _, _, out := depsFor(t, t.TempDir(), "root", "Root", testPassword, testPassword, "host")
	if code := runWith([]string{"admin", "setup", "--data", filepath.Join(t.TempDir(), "data")}, d); code == exitOK ||
		!strings.Contains(out.String(), "start garret serve once first") {
		t.Fatalf("setup without a CA exit %d:\n%s", code, out)
	}
}

func TestEnrollRejectsBadLink(t *testing.T) {
	d, _, _, out := depsFor(t, t.TempDir())
	if code := runWith([]string{"enroll", "http://localhost:8444/enroll"}, d); code != exitUsage || !strings.Contains(out.String(), "invitation link") {
		t.Fatalf("bad link exit %d:\n%s", code, out)
	}
}

func TestTerminalPrompterReadsLines(t *testing.T) {
	in := strings.NewReader("bob\n\nsecret words here\n")
	var out bytes.Buffer
	p := newPrompter(in, &out)
	if v, _ := p.Ask("Username", ""); v != "bob" {
		t.Fatalf("Ask = %q", v)
	}
	if v, _ := p.Ask("Device", "laptop"); v != "laptop" {
		t.Fatalf("default = %q", v)
	}
	if v, _ := p.Secret("Password"); v != "secret words here" {
		t.Fatalf("Secret = %q", v)
	}
	if _, err := p.Ask("More", ""); err == nil {
		t.Fatal("read past the end")
	}
	if !strings.Contains(out.String(), "Device [laptop]: ") {
		t.Fatalf("prompt output %q", out.String())
	}
}
