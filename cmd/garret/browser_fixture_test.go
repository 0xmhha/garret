package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// Environment of TestBrowserFixture: the file it describes itself in, and
// whether the server asks for client certificates ("off" turns them off).
const (
	browserStateEnv = "GARRET_E2E_STATE"
	browserCertsEnv = "GARRET_E2E_CLIENT_CERTS"
)

// browserUser is one simulated person as the browser suite signs in.
type browserUser struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Cert     string `json:"cert"`
	Key      string `json:"key"`
}

// browserState is what the Playwright suite (web/e2e) reads to drive steps
// 4 to 6 of the end-to-end scenario.
type browserState struct {
	URL         string                 `json:"url"`
	ClientCerts bool                   `json:"client_certs"`
	Project     string                 `json:"project"`
	Review      int                    `json:"review"`
	Users       map[string]browserUser `json:"users"`
	// Repo is bob's clone of the review branch with Git configured to push
	// as bob: certificate, CA and token in its local configuration.
	Repo string `json:"repo"`
	Home string `json:"home"`
}

// The review's file before the fix, numbered so that tests can name lines.
const (
	lenV1 = "#include <stddef.h>\n\nsize_t entries_len(size_t count, size_t size)\n{\n\treturn count * size;\n}\n"
	lenV2 = "#include <stddef.h>\n#include <stdint.h>\n\nsize_t entries_len(size_t count, size_t size)\n{\n\tif (size != 0 && count > SIZE_MAX / size)\n\t\treturn 0;\n\treturn count * size;\n}\n"
	testV = "#include \"../src/len.c\"\n\nint main(void) { return entries_len(2, 4) == 8 ? 0 : 1; }\n"
)

// TestBrowserFixture runs steps 1 to 3 of the end-to-end scenario
// (docs/design/testing.md) for the browser suite: a server with client
// certificates on (or off, see GARRET_E2E_CLIENT_CERTS), an administrator, bob and carol enrolled, and bob's
// review of three commits. It writes the state the suite needs to the file
// named by GARRET_E2E_STATE and keeps the server running until its standard
// input closes. Without that variable it is skipped.
func TestBrowserFixture(t *testing.T) {
	statePath := os.Getenv(browserStateEnv)
	if statePath == "" {
		t.Skip(browserStateEnv + " is not set; the browser suite starts this fixture")
	}
	clientCerts := os.Getenv(browserCertsEnv) != "off"
	s := startServerCerts(t, "", 0, 0, nil, clientCerts)
	port := mainPort(t, s.app)

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

	const project = "libfoo"
	if code, body := alice.call(t, http.MethodPost, "/api/v1/projects", map[string]string{"name": project, "description": "Length helpers"}); code != http.StatusCreated {
		t.Fatalf("create project %d %v", code, body)
	}
	_, tok := bob.call(t, http.MethodPost, "/api/v1/me/tokens", map[string]string{"name": "git"})
	token, _ := tok["secret"].(string)
	if token == "" {
		t.Fatal("no token")
	}

	repo := filepath.Join(t.TempDir(), "work")
	mustGit(t, bob, port, t.TempDir(), "", "", "init", repo)
	files := bob.files(port)
	remote := s.app.MainURL() + "/git/" + project + ".git"
	for _, kv := range [][2]string{
		{"user.name", "Bob"}, {"user.email", "bob@example.test"},
		{"http.sslCert", files.Cert}, {"http.sslKey", files.Key}, {"http.sslCAInfo", files.CA},
		{"http.extraHeader", "Authorization: Basic " + basicAuth("bob", token)},
		{"remote.origin.url", remote}, {"remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"},
	} {
		mustGit(t, bob, port, repo, "", "", "config", "--local", kv[0], kv[1])
	}
	write := func(name, text string) {
		p := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string, paths ...string) {
		mustGit(t, bob, port, repo, "", "", append([]string{"add"}, paths...)...)
		mustGit(t, bob, port, repo, "", "", "commit", "-m", msg)
	}
	write("README", "Length helpers\n")
	commit("Initial", "README")
	mustGit(t, bob, port, repo, "", "", "push", "origin", "main")
	mustGit(t, bob, port, repo, "", "", "checkout", "-b", "fix")
	write("src/len.c", lenV1)
	commit("Add entries_len", "src/len.c")
	write("src/len.c", lenV2)
	commit("Bound the count before multiplying", "src/len.c")
	write("tests/len_test.c", testV)
	commit("Test entries_len", "tests/len_test.c")
	mustGit(t, bob, port, repo, "", "", "push", "origin", "fix")
	code, rv := bob.call(t, http.MethodPost, "/api/v1/projects/"+project+"/reviews",
		map[string]string{"source_branch": "refs/heads/fix", "target_branch": "refs/heads/main", "title": "Bound the length field before multiplying"})
	if code != http.StatusCreated {
		t.Fatalf("open review %d %v", code, rv)
	}

	user := func(m *member, username string) browserUser {
		f := m.files(port)
		return browserUser{Username: username, Password: testPassword, Cert: f.Cert, Key: f.Key}
	}
	state := browserState{URL: s.app.MainURL(), ClientCerts: clientCerts, Project: project, Review: 1, Repo: repo, Home: bob.home,
		Users: map[string]browserUser{"admin": user(alice, "root"), "bob": user(bob, "bob"), "carol": user(carol, "carol")}}
	writeState(t, statePath, state)

	// The suite closes standard input when it is done.
	_, _ = io.Copy(io.Discard, os.Stdin)
}

// writeState writes the state file in one step, so the suite never reads
// half of it.
func writeState(t *testing.T, path string, state browserState) {
	t.Helper()
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

// basicAuth is the value of an HTTP Basic Authorization header.
func basicAuth(username, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
}
