package enroll

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// keychainTestEnv turns on the test that runs the real security commands.
// It runs in CI only: it creates a keychain, which a developer's machine
// should not get as a side effect of go test.
const keychainTestEnv = "GARRET_KEYCHAIN_TEST"

// TestKeychainStepsOnMacOS runs the import, identity preference and
// removal steps against a throwaway keychain at the path the steps use for
// the login keychain. Trusting the CA needs an interactive password and is
// left to the real-machine run.
func TestKeychainStepsOnMacOS(t *testing.T) {
	if os.Getenv(keychainTestEnv) != "1" {
		t.Skip("set " + keychainTestEnv + "=1 to run against a temporary keychain")
	}
	home := t.TempDir()
	p := Platform{GOOS: goosDarwin, Home: home}
	kc := keychain(p)
	if err := os.MkdirAll(filepath.Dir(kc), 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(context.Background(), "security", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("security %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	// A throwaway keychain needs some password; an empty one fails to
	// unlock on the CI runner.
	const pass = "garret-test-keychain"
	run("create-keychain", "-p", pass, kc)
	t.Cleanup(func() { _ = exec.CommandContext(context.Background(), "security", "delete-keychain", kc).Run() })
	run("unlock-keychain", "-p", pass, kc)

	_, ca := testServer(t, enrollHandler)
	files := FilesIn(filepath.Join(home, "client"))
	key, _ := NewKey()
	cert := signFor(t, ca, key)
	if err := files.Save(key, cert, ca.Cert); err != nil {
		t.Fatal(err)
	}
	steps := append(importSteps(p, files), preferenceStep(p, testURL, cert))
	if err := Apply(context.Background(), steps, ExecRunner{}, os.Stderr, false); err != nil {
		t.Fatal(err)
	}
	hash := certSHA1(cert)
	if out := run("find-identity", kc); !strings.Contains(out, hash) {
		t.Fatalf("identity %s not in keychain:\n%s", hash, out)
	}
	if out := run("get-identity-preference", "-s", testURL+"/", "-Z", kc); !strings.Contains(out, hash) {
		t.Fatalf("preference does not name %s:\n%s", hash, out)
	}

	newKey, _ := NewKey()
	newCert := signFor(t, ca, newKey)
	if err := files.Save(newKey, newCert, ca.Cert); err != nil {
		t.Fatal(err)
	}
	renew, err := RenewSteps(p, files, testURL, newCert, cert)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(context.Background(), renew, ExecRunner{}, os.Stderr, false); err != nil {
		t.Fatal(err)
	}
	out := run("find-identity", kc)
	if !strings.Contains(out, certSHA1(newCert)) || strings.Contains(out, hash) {
		t.Fatalf("after renewal:\n%s", out)
	}
	if out := run("get-identity-preference", "-s", testURL+"/", "-Z", kc); !strings.Contains(out, certSHA1(newCert)) {
		t.Fatalf("preference after renewal:\n%s", out)
	}
}
