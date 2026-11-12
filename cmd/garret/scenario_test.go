package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xmhha/garret/internal/datadir"
	"github.com/0xmhha/garret/internal/enroll"
	"github.com/0xmhha/garret/internal/pki"
)

// outsiders presents the next accepted connections as coming from an
// address outside the allowed network, so the network filter sees them.
type outsiders struct {
	net.Listener
	next *atomic.Int32
}

var outsideAddr = &net.TCPAddr{IP: net.IPv4(10, 9, 9, 9), Port: 40000}

func (o outsiders) Accept() (net.Conn, error) {
	c, err := o.Listener.Accept()
	if err == nil && o.next.Load() > 0 {
		o.next.Add(-1)
		return outsideConn{c}, nil
	}
	return c, err
}

type outsideConn struct{ net.Conn }

func (outsideConn) RemoteAddr() net.Addr { return outsideAddr }

// member is one simulated person with a home directory and a session.
type member struct {
	name   string
	home   string
	base   string
	client *http.Client
	cookie *http.Cookie
	csrf   string
}

func (m *member) files(port string) enroll.Files { return enroll.FilesIn(clientDir(m.home, port)) }

// tlsClient trusts ca and presents the certificate in files, if any.
func tlsClient(ca *x509.Certificate, files *enroll.Files) *http.Client {
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	cfg := &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS13}
	if files != nil {
		if key, cert, err := files.Load(); err == nil {
			cfg.Certificates = []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key, Leaf: cert}}
		}
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: cfg}}
}

// login opens a session with the member's current certificate.
func (m *member) login(t *testing.T, s *testServer, port string) {
	t.Helper()
	m.base = s.app.MainURL()
	c, cookie, csrf := session(t, s.app, clientDir(m.home, port), m.name)
	m.client, m.cookie, m.csrf = c, cookie, csrf
}

// call sends a JSON request in the member's session and decodes the reply.
func (m *member) call(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, m.base+path, r)
	req.Header.Set("Origin", m.base)
	req.Header.Set("X-CSRF-Token", m.csrf)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(m.cookie)
	resp, err := m.client.Do(req)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// git runs git for member m with its certificate, and with token (when not
// empty) as the password for username.
func (m *member) git(t *testing.T, port, dir, username, token string, args ...string) error {
	t.Helper()
	f := m.files(port)
	full := []string{"-c", "http.sslCert=" + f.Cert, "-c", "http.sslKey=" + f.Key, "-c", "http.sslCAInfo=" + f.CA,
		"-c", "user.name=" + m.name, "-c", "user.email=" + m.name + "@example.test", "-c", "init.defaultBranch=main"}
	if token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte(username + ":" + token))
		full = append(full, "-c", "http.extraHeader=Authorization: Basic "+basic)
	}
	cmd := exec.CommandContext(context.Background(), "git", append(full, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + m.home, "PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0", "LANG=C"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("git %s: %s", strings.Join(args, " "), out)
	}
	return err
}

func mustGit(t *testing.T, m *member, port, dir, username, token string, args ...string) {
	t.Helper()
	if err := m.git(t, port, dir, username, token, args...); err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
}

func enrollMember(t *testing.T, name, link, code string) *member {
	t.Helper()
	m := &member{name: name, home: t.TempDir()}
	d, _, _, out := depsFor(t, m.home, code, name, strings.ToUpper(name[:1])+name[1:], testPassword, testPassword, name+"-laptop")
	if c := runWith([]string{"enroll", link}, d); c != exitOK {
		t.Fatalf("enroll %s exit %d:\n%s", name, c, out)
	}
	return m
}

// TestScenario runs steps 1, 2, 3, 7 and 8 of the end-to-end scenario
// (docs/design/testing.md).
func TestScenario(t *testing.T) {
	var next atomic.Int32
	listen := func(ctx context.Context, address string) (net.Listener, error) {
		l, err := new(net.ListenConfig).Listen(ctx, "tcp", address)
		return outsiders{Listener: l, next: &next}, err
	}

	// Step 1: the server starts and prints the allowed network and the CA
	// fingerprint.
	s := startServer(t, "", 0, 0, listen)
	port := mainPort(t, s.app)
	for _, want := range []string{"allowed network 192.168.1.0/24", "CA fingerprint (SHA-256) " + s.app.Fingerprint()} {
		if !strings.Contains(s.out.String(), want) {
			t.Fatalf("banner lacks %q:\n%s", want, s.out)
		}
	}

	// Step 2: the administrator invites two members, who enroll.
	alice := &member{name: "root", home: t.TempDir()}
	d, _, _, out := depsFor(t, alice.home, "root", "Alice", testPassword, testPassword, "server")
	if c := runWith([]string{"admin", "setup", "--data", s.dataDir, "--host", "localhost", "--port", port}, d); c != exitOK {
		t.Fatalf("admin setup exit %d:\n%s", c, out)
	}
	alice.login(t, s, port)
	linkB, codeB := invite(t, s.app, alice.client, alice.cookie, alice.csrf)
	linkC, codeC := invite(t, s.app, alice.client, alice.cookie, alice.csrf)
	bob := enrollMember(t, "bob", linkB, codeB)
	carol := enrollMember(t, "carol", linkC, codeC)
	bob.login(t, s, port)
	carol.login(t, s, port)

	// Step 3: bob pushes a branch with three commits and opens a review.
	if code, body := alice.call(t, http.MethodPost, "/api/v1/projects", map[string]string{"name": "fix", "description": "the fix"}); code != http.StatusCreated {
		t.Fatalf("create project %d %v", code, body)
	}
	_, tok := bob.call(t, http.MethodPost, "/api/v1/me/tokens", map[string]string{"name": "git"})
	bobToken, _ := tok["secret"].(string)
	_, tok = carol.call(t, http.MethodPost, "/api/v1/me/tokens", map[string]string{"name": "git"})
	carolToken, _ := tok["secret"].(string)
	if bobToken == "" || carolToken == "" {
		t.Fatal("no tokens")
	}
	repo := filepath.Join(t.TempDir(), "work")
	remote := s.app.MainURL() + "/git/fix.git"
	mustGit(t, bob, port, t.TempDir(), "", "", "init", repo)
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("README", "garret test\n")
	mustGit(t, bob, port, repo, "", "", "add", "README")
	mustGit(t, bob, port, repo, "", "", "commit", "-m", "initial")
	mustGit(t, bob, port, repo, "bob", bobToken, "push", remote, "main")
	mustGit(t, bob, port, repo, "", "", "checkout", "-b", "fix")
	for i, f := range []string{"a.txt", "b.txt", "c.txt"} {
		write(f, strings.Repeat("x", i+1)+"\n")
		mustGit(t, bob, port, repo, "", "", "add", f)
		mustGit(t, bob, port, repo, "", "", "commit", "-m", "change "+f)
	}
	mustGit(t, bob, port, repo, "bob", bobToken, "push", remote, "fix")
	code, rv := bob.call(t, http.MethodPost, "/api/v1/projects/fix/reviews", map[string]string{"source_branch": "refs/heads/fix", "target_branch": "refs/heads/main", "title": "Fix it"})
	if code != http.StatusCreated || rv["number"] != float64(1) {
		t.Fatalf("open review %d %v", code, rv)
	}
	if n := reviewCommits(t, carol); n != 3 {
		t.Fatalf("review has %d commits, want 3", n)
	}

	// Step 7: what must be rejected is rejected.
	bobFiles := bob.files(port)
	next.Store(1)
	if resp, err := get(tlsClient(s.app.CA(), &bobFiles), s.app.MainURL()+"/api/v1/me"); err == nil {
		_ = resp.Body.Close()
		t.Fatal("a connection from outside the allowed network was served")
	}
	next.Store(0)
	if resp, err := get(tlsClient(s.app.CA(), nil), s.app.MainURL()+"/"); err == nil {
		_ = resp.Body.Close()
		t.Fatal("a device without a client certificate was served")
	}
	expired := expiredFiles(t, s, bob, port)
	if resp, err := get(tlsClient(s.app.CA(), &expired), s.app.MainURL()+"/"); err == nil {
		_ = resp.Body.Close()
		t.Fatal("an expired client certificate was served")
	}
	if err := bob.git(t, port, repo, "", "", "push", remote, "fix:refs/heads/no-token"); err == nil {
		t.Fatal("a push without a token was accepted")
	}
	if err := bob.git(t, port, repo, "carol", carolToken, "push", remote, "fix:refs/heads/wrong-user"); err == nil {
		t.Fatal("a push with another user's token was accepted")
	}
	// A revoked certificate stops working.
	_, certs := carol.call(t, http.MethodGet, "/api/v1/me/certificates", nil)
	serial := currentSerial(t, certs)
	if code, body := carol.call(t, http.MethodDelete, "/api/v1/me/certificates/"+serial, nil); code != http.StatusNoContent {
		t.Fatalf("revoke %d %v", code, body)
	}
	// RevokeCertificate replaces the active set before it returns, and the
	// API checks it on every request, so the open connection fails now.
	if code, _ := carol.call(t, http.MethodGet, "/api/v1/me", nil); code == http.StatusOK {
		t.Fatal("a revoked certificate was served")
	}

	// Step 8: everything survives a restart.
	if err := s.stop(); err != nil {
		t.Fatal(err)
	}
	s = startServer(t, s.dataDir, s.mainPort, s.enrollPort, nil)
	bob.login(t, s, port)
	if n := reviewCommits(t, bob); n != 3 {
		t.Fatalf("after restart the review has %d commits", n)
	}
	_, users := bob.call(t, http.MethodGet, "/api/v1/users", nil)
	for _, name := range []string{"root", "bob", "carol"} {
		if !strings.Contains(toJSON(users), `"`+name+`"`) {
			t.Errorf("user %s missing after restart: %s", name, toJSON(users))
		}
	}
	clone := filepath.Join(t.TempDir(), "clone")
	if err := bob.git(t, port, t.TempDir(), "bob", bobToken, "clone", s.app.MainURL()+"/git/fix.git", clone); err != nil {
		t.Fatal("the token or the repository did not survive the restart")
	}

	// Step 7, renewal (after the restart, because it moves the clock
	// forward): a device with a valid certificate renews it, and one with
	// an expired certificate cannot.
	testClock.advance(4 * 24 * time.Hour)
	t.Cleanup(func() { testClock.advance(-4 * 24 * time.Hour) })
	d, _, _, out = depsFor(t, bob.home)
	if c := runWith([]string{"renew"}, d); c != exitOK || !strings.Contains(out.String(), "renewed until") {
		t.Fatalf("renew exit %d:\n%s", c, out)
	}
	bob.login(t, s, port)
	// A device with an expired certificate cannot.
	stale := &member{name: "bob", home: t.TempDir()}
	staleFiles := stale.files(port)
	key, cert, _ := expired.Load()
	ca, _ := expired.LoadCA()
	if err := staleFiles.Save(key, cert, ca); err != nil {
		t.Fatal(err)
	}
	d, _, _, out = depsFor(t, stale.home)
	if c := runWith([]string{"renew"}, d); c == exitOK || !strings.Contains(out.String(), "expired") {
		t.Fatalf("renew with an expired certificate exit %d:\n%s", c, out)
	}
}

func reviewCommits(t *testing.T, m *member) int {
	t.Helper()
	code, body := m.call(t, http.MethodGet, "/api/v1/projects/fix/reviews/1", nil)
	if code != http.StatusOK {
		t.Fatalf("get review %d %v", code, body)
	}
	revs, _ := body["revisions"].([]any)
	if len(revs) == 0 {
		t.Fatalf("no revisions: %v", body)
	}
	commits, _ := revs[len(revs)-1].(map[string]any)["commits"].([]any)
	return len(commits)
}

func currentSerial(t *testing.T, body map[string]any) string {
	t.Helper()
	list, _ := body["certificates"].([]any)
	for _, c := range list {
		if m, _ := c.(map[string]any); m["current"] == true {
			s, _ := m["serial"].(string)
			return s
		}
	}
	t.Fatalf("no current certificate: %v", body)
	return ""
}

// expiredFiles writes, for member m, a key and a certificate from the
// server's CA that expired a day ago.
func expiredFiles(t *testing.T, s *testServer, m *member, port string) enroll.Files {
	t.Helper()
	dir, err := datadir.Open(s.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := pki.LoadCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := enroll.NewKey()
	serial, _ := pki.NewSerial(rand.Reader)
	tmpl, _ := pki.ClientTemplate(serial, testClock.Now().Add(-8*24*time.Hour), "user-"+m.name)
	cert, err := pki.Sign(tmpl, ca.Cert, &key.PublicKey, ca.Key, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	files := enroll.FilesIn(filepath.Join(t.TempDir(), "expired"))
	if err := files.Save(key, cert, ca.Cert); err != nil {
		t.Fatal(err)
	}
	_ = port
	return files
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func get(c *http.Client, url string) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	return c.Do(req)
}
