package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/clock"
	"github.com/0xmhha/garret/internal/config"
	"github.com/0xmhha/garret/internal/enroll"
	"github.com/0xmhha/garret/internal/pki"
)

// requestBudget bounds the network part of one enrollment or renewal; the
// prompts before it are not bounded.
const requestBudget = 2 * time.Minute

// deps is what the client commands take from the machine.
type deps struct {
	stdout, stderr io.Writer
	prompt         Prompter
	runner         enroll.Runner
	clock          clock.Clock
	hostname       string
	platform       enroll.Platform
}

func realDeps(stdout, stderr io.Writer) (deps, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return deps{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return deps{}, err
	}
	host, _ := os.Hostname()
	return deps{stdout: stdout, stderr: stderr, prompt: newPrompter(os.Stdin, stdout), runner: enroll.ExecRunner{},
		clock: clock.System{}, hostname: host,
		platform: enroll.Platform{GOOS: runtime.GOOS, Home: home, XDGConfig: os.Getenv("XDG_CONFIG_HOME"), Executable: exe, UID: os.Getuid()}}, nil
}

func fail(d deps, cmd string, err error) int {
	_, _ = fmt.Fprintf(d.stderr, "garret %s: %v\n", cmd, err)
	return exitError
}

// account asks for a new account's fields. The password is asked twice
// and checked here, before anything is sent.
func account(p Prompter, hostname string) (auth.NewAccount, error) {
	var a auth.NewAccount
	var err error
	if a.Username, err = p.Ask("Username", ""); err != nil {
		return a, err
	}
	if a.DisplayName, err = p.Ask("Display name", a.Username); err != nil {
		return a, err
	}
	if a.Password, err = p.Secret("Password"); err != nil {
		return a, err
	}
	again, err := p.Secret("Password again")
	if err != nil {
		return a, err
	}
	if again != a.Password {
		return a, errors.New("the passwords do not match")
	}
	if err := auth.ValidPassword(a.Password); err != nil {
		return a, err
	}
	a.Device, err = p.Ask("Device name", hostname)
	return a, err
}

// filesFor returns the client files for serverURL on this device.
func filesFor(d deps, serverURL string) (enroll.Files, error) {
	host, port, err := hostPort(serverURL)
	if err != nil {
		return enroll.Files{}, err
	}
	p := d.platform
	return enroll.FilesIn(enroll.ClientDir(p.GOOS, p.Home, p.XDGConfig, host, port)), nil
}

func hostPort(serverURL string) (string, uint16, error) {
	u, err := url.Parse(serverURL)
	if err != nil || u.Port() == "" {
		return "", 0, fmt.Errorf("bad server URL %q", serverURL)
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil {
		return "", 0, fmt.Errorf("bad server URL %q", serverURL)
	}
	return u.Hostname(), uint16(port), nil
}

// install saves the key, certificate and CA for serverURL and applies the
// installation steps.
func install(ctx context.Context, d deps, cmd, serverURL string, key *ecdsa.PrivateKey, res enroll.Result) int {
	files, err := filesFor(d, serverURL)
	if err != nil {
		return fail(d, cmd, err)
	}
	if err := files.Save(key, res.Certificate, res.CA); err != nil {
		return fail(d, cmd, err)
	}
	_, _ = fmt.Fprintf(d.stdout, "Saved the key and certificate in %s\n", files.Dir)
	steps, err := enroll.InstallSteps(d.platform, files, serverURL, res.Certificate)
	if err != nil {
		return fail(d, cmd, err)
	}
	if err := enroll.Apply(ctx, steps, d.runner, d.stdout, false); err != nil {
		return fail(d, cmd, err)
	}
	_, _ = fmt.Fprintf(d.stdout, "Done. Open %s\n", serverURL)
	return exitOK
}

func enrollCmd(args []string, d deps) int {
	fs := flag.NewFlagSet("enroll", flag.ContinueOnError)
	fs.SetOutput(d.stderr)
	dryRun := fs.Bool("dry-run", false, "print the steps without enrolling or changing anything")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(d.stderr, "usage: garret enroll [--dry-run] <invitation link>")
		return exitUsage
	}
	link, err := enroll.ParseLink(fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintf(d.stderr, "garret enroll: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestBudget)
	ca, err := enroll.FetchCA(ctx, link)
	cancel()
	if err != nil {
		return fail(d, "enroll", err)
	}
	_, _ = fmt.Fprintf(d.stdout, "The server's CA matches the invitation link (%s).\n", pki.Fingerprint(ca))
	if *dryRun {
		return dryRunEnroll(d, link)
	}
	code, err := d.prompt.Secret("Invitation code")
	if err != nil {
		return fail(d, "enroll", err)
	}
	acct, err := account(d.prompt, d.hostname)
	if err != nil {
		return fail(d, "enroll", err)
	}
	key, err := enroll.NewKey()
	if err != nil {
		return fail(d, "enroll", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), requestBudget)
	defer cancel()
	res, err := enroll.Enroll(ctx, link, ca, enroll.Request{Code: code, Username: acct.Username, DisplayName: acct.DisplayName,
		Password: acct.Password, Device: acct.Device}, key)
	if err != nil {
		return fail(d, "enroll", err)
	}
	_, _ = fmt.Fprintf(d.stdout, "Enrolled as %s.\n", acct.Username)
	return install(ctx, d, "enroll", res.ServerURL, key, res)
}

// dryRunEnroll prints the steps for the default main port; the real run
// uses the URL the server returns.
func dryRunEnroll(d deps, link enroll.Link) int {
	serverURL := "https://" + net.JoinHostPort(link.Host, strconv.Itoa(config.DefaultPort))
	files, err := filesFor(d, serverURL)
	if err != nil {
		return fail(d, "enroll", err)
	}
	steps, err := enroll.InstallSteps(d.platform, files, serverURL, nil)
	if err != nil {
		return fail(d, "enroll", err)
	}
	_, _ = fmt.Fprintf(d.stdout, "would: ask for the invitation code and the account, and send them to %s\n", link.Base())
	_, _ = fmt.Fprintf(d.stdout, "would: save the key, certificate and CA in %s\n", files.Dir)
	if err := enroll.Apply(context.Background(), steps, d.runner, d.stdout, true); err != nil {
		return fail(d, "enroll", err)
	}
	return exitOK
}

func renewCmd(args []string, d deps) int {
	fs := flag.NewFlagSet("renew", flag.ContinueOnError)
	fs.SetOutput(d.stderr)
	server := fs.String("server", "", "renew only this server (its URL)")
	force := fs.Bool("force", false, "renew even when the certificate is not due")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return exitUsage
	}
	servers, err := installedServers(d, *server)
	if err != nil {
		return fail(d, "renew", err)
	}
	if len(servers) == 0 {
		_, _ = fmt.Fprintln(d.stdout, "No garret server is installed on this device.")
		return exitOK
	}
	code := exitOK
	for _, s := range servers {
		if err := renewOne(d, s, *force); err != nil {
			_, _ = fmt.Fprintf(d.stderr, "garret renew %s: %v\n", s, err)
			code = exitError
		}
	}
	return code
}

// installedServers returns serverURL alone, or one URL per client
// directory, sorted by name.
func installedServers(d deps, serverURL string) ([]string, error) {
	if serverURL != "" {
		return []string{strings.TrimSuffix(serverURL, "/")}, nil
	}
	p := d.platform
	base := filepath.Dir(enroll.ClientDir(p.GOOS, p.Home, p.XDGConfig, "x", 1))
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		i := strings.LastIndexByte(e.Name(), '-')
		if !e.IsDir() || i <= 0 {
			continue
		}
		port, err := strconv.ParseUint(e.Name()[i+1:], 10, 16)
		if err != nil || port == 0 {
			continue // a staging directory or something garret did not write
		}
		out = append(out, "https://"+net.JoinHostPort(e.Name()[:i], strconv.FormatUint(port, 10)))
	}
	return out, nil
}

var errExpired = errors.New("the certificate has expired; ask an administrator for a new invitation and run garret enroll")

func renewOne(d deps, serverURL string, force bool) error {
	files, err := filesFor(d, serverURL)
	if err != nil {
		return err
	}
	key, cert, err := files.Load()
	if err != nil {
		return err
	}
	ca, err := files.LoadCA()
	if err != nil {
		return err
	}
	now := d.clock.Now()
	if !now.Before(cert.NotAfter) {
		return errExpired
	}
	if !force && !pki.RenewalDue(cert.NotBefore, cert.NotAfter, now) {
		_, _ = fmt.Fprintf(d.stdout, "%s: not due until %s\n", serverURL, cert.NotBefore.Add(cert.NotAfter.Sub(cert.NotBefore)/2).Format(time.RFC3339))
		return nil
	}
	newKey, err := enroll.NewKey()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestBudget)
	defer cancel()
	res, err := enroll.Renew(ctx, serverURL, ca, tls.Certificate{Certificate: [][]byte{cert.Raw}, PrivateKey: key, Leaf: cert}, newKey)
	if err != nil {
		return err
	}
	if err := files.Save(newKey, res.Certificate, ca); err != nil {
		return err
	}
	steps, err := enroll.RenewSteps(d.platform, files, serverURL, res.Certificate, cert)
	if err != nil {
		return err
	}
	if err := enroll.Apply(ctx, steps, d.runner, d.stdout, false); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(d.stdout, "%s: renewed until %s\n", serverURL, res.Certificate.NotAfter.Format(time.RFC3339))
	return nil
}
