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
	// A name ending in .keychain: create-keychain then unlock-keychain on a
	// path ending in .keychain-db failed on the CI runner.
	kc := filepath.Join(home, "garret-test.keychain")
	p := Platform{GOOS: goosDarwin, Home: home, Keychain: kc}
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(context.Background(), "security", args...).CombinedOutput()
		if err != nil {
			entries, _ := os.ReadDir(home)
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Fatalf("security %s: %v: %s (home has %v)", strings.Join(args, " "), err, out, names)
		}
		return string(out)
	}
	// A throwaway keychain needs some password; an empty one fails to
	// unlock on the CI runner.
	const pass = "garret-test-keychain"
	run("create-keychain", "-p", pass, kc)
	t.Cleanup(func() { _ = exec.CommandContext(context.Background(), "security", "delete-keychain", kc).Run() })
	run("unlock-keychain", "-p", pass, kc)
	// Identity preferences live in the keychain search list, as the login
	// keychain does on a real machine. Put the throwaway keychain first for
	// the test and restore the list afterwards.
	before := strings.Fields(strings.ReplaceAll(run("list-keychains", "-d", "user"), `"`, ""))
	defaultKC := strings.TrimSpace(strings.ReplaceAll(run("default-keychain", "-d", "user"), `"`, ""))
	run(append([]string{"list-keychains", "-d", "user", "-s", kc}, before...)...)
	run("default-keychain", "-d", "user", "-s", kc)
	t.Cleanup(func() {
		ctx := context.Background()
		_ = exec.CommandContext(ctx, "security", append([]string{"list-keychains", "-d", "user", "-s"}, before...)...).Run()
		if defaultKC != "" {
			_ = exec.CommandContext(ctx, "security", "default-keychain", "-d", "user", "-s", defaultKC).Run()
		}
	})

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
