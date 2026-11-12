package gitserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/gitrepo"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/inbox"
	"github.com/0xmhha/garret/internal/pki"
	"github.com/0xmhha/garret/internal/project"
	"github.com/0xmhha/garret/internal/realtime"
	"github.com/0xmhha/garret/internal/review"
	"github.com/0xmhha/garret/internal/store"
)

type wall struct{}

func (wall) Now() time.Time { return time.Now() }

type nopEvents struct{}

func (nopEvents) Publish(realtime.Event) {}

type env struct {
	url     string
	token   string
	reviews *review.Service
	admin   auth.User
	gitPath string
	base    string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	base := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(base, "garret.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ids, _ := id.NewGenerator(wall{}, rand.Reader)
	rec := audit.NewRecorder(ids, wall{})
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := pki.NewSerial(rand.Reader)
	tmpl, _ := pki.CATemplate(serial, time.Now(), pki.Names{Host: "review.local", Networks: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}})
	caCert, _ := pki.Sign(tmpl, tmpl, &key.PublicKey, key, rand.Reader)
	authSvc, _ := auth.NewService(auth.Config{DB: db, Audit: rec, IDs: ids, Clock: wall{}, Rand: rand.Reader,
		CA: &pki.CA{Cert: caCert, Key: key}, Active: &pki.ActiveSerials{},
		Password: auth.Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}, ClientCertsOff: true})
	setup, _ := authSvc.SetupToken()
	k2, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csr, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, k2)
	admin, err := authSvc.SetupAdmin(ctx, setup, auth.NewAccount{Username: "root", DisplayName: "Root", Password: "correct horse battery staple", Device: "host"}, csr)
	if err != nil {
		t.Fatal(err)
	}
	_, token, _ := authSvc.CreateToken(ctx, admin.User, "git")
	runner, _ := gitrepo.NewRunner(ctx, gitPath, 2, base)
	repos := filepath.Join(base, "repos")
	_ = os.Mkdir(repos, 0o700)
	projects := project.New(db, rec, ids, wall{}, runner, repos)
	if _, err := projects.Create(ctx, admin.User, "libfoo", ""); err != nil {
		t.Fatal(err)
	}
	reviews := review.NewService(db, rec, ids, wall{}, runner, projects, nopEvents{}, inbox.NewNotifier(ids, wall{}))
	h := New(Config{Auth: authSvc, Projects: projects, Reviews: reviews, Runner: runner, Log: slog.New(slog.DiscardHandler), MaxPush: 1 << 20})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &env{url: srv.URL, token: token, reviews: reviews, admin: admin.User, gitPath: gitPath, base: base}
}

func (e *env) git(t *testing.T, dir string, wantOK bool, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.gitPath, args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_NAME=R", "GIT_AUTHOR_EMAIL=r@example.invalid", "GIT_COMMITTER_NAME=R", "GIT_COMMITTER_EMAIL=r@example.invalid"}
	out, err := cmd.CombinedOutput()
	if wantOK && err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	if !wantOK && err == nil {
		t.Fatalf("git %v succeeded, want failure\n%s", args, out)
	}
	return string(out)
}

func (e *env) remote(user, token string) string {
	u, _ := url.Parse(e.url + "/git/libfoo.git")
	u.User = url.UserPassword(user, token)
	return u.String()
}

func TestPushCloneAndReviewRevision(t *testing.T) {
	e := newEnv(t)
	wt := filepath.Join(e.base, "wt")
	_ = os.Mkdir(wt, 0o700)
	e.git(t, wt, true, "init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(wt, "a.txt"), []byte("a\n"), 0o600)
	e.git(t, wt, true, "add", ".")
	e.git(t, wt, true, "commit", "-q", "-m", "base")
	e.git(t, wt, true, "push", "-q", e.remote("root", e.token), "main")
	e.git(t, wt, true, "checkout", "-q", "-b", "fix")
	_ = os.WriteFile(filepath.Join(wt, "a.txt"), []byte("b\n"), 0o600)
	e.git(t, wt, true, "commit", "-q", "-am", "fix")
	e.git(t, wt, true, "push", "-q", e.remote("root", e.token), "fix")
	if _, err := e.reviews.Open(context.Background(), e.admin, "libfoo", "refs/heads/fix", "refs/heads/main", "Fix"); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(wt, "a.txt"), []byte("c\n"), 0o600)
	e.git(t, wt, true, "commit", "-q", "-am", "more")
	e.git(t, wt, true, "push", "-q", e.remote("root", e.token), "fix")
	d, err := e.reviews.Get(context.Background(), e.admin, "libfoo", 1)
	if err != nil || len(d.Revisions) != 2 {
		t.Fatalf("push did not create revision 2: %+v, %v", d.Revisions, err)
	}
	clone := filepath.Join(e.base, "clone")
	e.git(t, e.base, true, "clone", "-q", e.remote("root", e.token), clone)
	if refs := e.git(t, clone, true, "ls-remote", e.remote("root", e.token)); strings.Contains(refs, "refs/garret") {
		t.Fatalf("hidden refs advertised: %s", refs)
	}
	if out := e.git(t, wt, false, "push", e.remote("root", e.token), "HEAD:refs/garret/reviews/x/revisions/9"); !strings.Contains(out, "refs/garret") {
		t.Fatalf("push to a hidden ref: %s", out)
	}
}

func TestAuthentication(t *testing.T) {
	e := newEnv(t)
	for name, remote := range map[string]string{
		"no credentials": e.url + "/git/libfoo.git",
		"wrong token":    e.remote("root", "wrong"),
		"wrong user":     e.remote("bob", e.token),
	} {
		out := e.git(t, e.base, false, "ls-remote", remote)
		if !strings.Contains(out, "401") && !strings.Contains(strings.ToLower(out), "authentication") && !strings.Contains(out, "could not read Username") {
			t.Errorf("%s: %s", name, out)
		}
	}
}

func TestRequestValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for path, want := range map[string]int{
		"/git/libfoo.git/objects/info/packs":                 http.StatusNotFound,
		"/git/libfoo.git/info/refs":                          http.StatusBadRequest,
		"/git/libfoo.git/info/refs?service=git-shell":        http.StatusBadRequest,
		"/git/missing.git/info/refs?service=git-upload-pack": http.StatusNotFound,
		"/git/../x.git/info/refs?service=git-upload-pack":    http.StatusNotFound,
	} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.url+path, nil)
		req.SetBasicAuth("root", e.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, resp.StatusCode, want)
		}
	}
}
