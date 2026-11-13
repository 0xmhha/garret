package app

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/garret/internal/clock"
	"github.com/0xmhha/garret/internal/config"
	"github.com/0xmhha/garret/internal/diskcrypt"
	"github.com/0xmhha/garret/internal/netguard"
	"github.com/0xmhha/garret/internal/webui"
)

func freePort(t *testing.T) uint16 {
	t.Helper()
	l, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return uint16(l.Addr().(*net.TCPAddr).Port)
}

func testSystem(t *testing.T, out io.Writer) System {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	return System{
		Clock: clock.System{}, Rand: rand.Reader, GitPath: git, Log: discard(), Out: out,
		Interfaces: func() ([]netguard.Interface, error) {
			return []netguard.Interface{{Name: "en0", Up: true, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.20/24")}}}, nil
		},
	}
}

func testConfig(t *testing.T, clientCerts bool) config.Serve {
	t.Helper()
	enroll := freePort(t)
	return config.Serve{
		DataDir: filepath.Join(t.TempDir(), "data"), Host: "localhost",
		Listen:       netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 0),
		EnrollListen: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), enroll),
		Port:         config.DefaultPort, EnrollPort: enroll, ClientCerts: clientCerts,
	}
}

// start builds the app and runs it until the test ends.
func start(t *testing.T, cfg config.Serve) (*App, *strings.Builder, func() error) {
	t.Helper()
	var out strings.Builder
	a, err := New(context.Background(), cfg, testSystem(t, &out))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	stop := sync.OnceValue(func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(20 * time.Second):
			return errors.New("Run did not return")
		}
	})
	t.Cleanup(func() { _ = stop() })
	return a, &out, stop
}

func clientFor(a *App, cert *tls.Certificate) *http.Client {
	pool := x509.NewCertPool()
	pool.AddCert(a.CA())
	cfg := &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS13}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
	}
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: cfg}}
}

func get(c *http.Client, url string) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	return c.Do(req)
}

func TestAppRequiresClientCertificates(t *testing.T) {
	a, out, stop := start(t, testConfig(t, true))
	for _, want := range []string{a.MainURL(), "192.168.1.0/24", a.Fingerprint()} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("banner lacks %q:\n%s", want, out.String())
		}
	}
	if resp, err := get(clientFor(a, nil), a.MainURL()+"/"); err == nil {
		_ = resp.Body.Close()
		t.Fatal("request without a client certificate was served")
	}
	if err := stop(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestAppServesWithCertificatesOff(t *testing.T) {
	cfg := testConfig(t, false)
	a, out, stop := start(t, cfg)
	if notice := strings.Contains(out.String(), "web interface: not built"); notice == webui.Built() {
		t.Errorf("not-built notice %v with the interface built %v:\n%s", notice, webui.Built(), out.String())
	}
	if !strings.Contains(out.String(), "client certificates are off") {
		t.Errorf("no warning:\n%s", out.String())
	}
	resp, err := get(clientFor(a, nil), a.MainURL()+"/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "<title>garret</title>") {
		t.Fatalf("placeholder %d %s", resp.StatusCode, body)
	}
	resp, err = get(clientFor(a, nil), a.MainURL()+"/api/v1/me")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("api without session %d", resp.StatusCode)
	}
	// No invitation: the enrollment port is closed.
	if c, err := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "tcp", "127.0.0.1:"+strconv.Itoa(int(cfg.EnrollListen.Port()))); err == nil {
		_ = c.Close()
		t.Fatal("enrollment listener open without invitations")
	}
	if err := stop(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if resp, err := get(clientFor(a, nil), a.MainURL()+"/"); err == nil {
		_ = resp.Body.Close()
		t.Fatal("still serving after shutdown")
	}
}

func TestNewFailsOnBadDataDirectory(t *testing.T) {
	cfg := testConfig(t, true)
	cfg.DataDir = "/dev/null/garret"
	if _, err := New(context.Background(), cfg, testSystem(t, io.Discard)); err == nil {
		t.Fatal("New accepted an unusable data directory")
	}
}

func TestNewFailsWhenThePortIsTaken(t *testing.T) {
	l, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	cfg := testConfig(t, true)
	cfg.Listen = netip.MustParseAddrPort(l.Addr().String())
	if _, err := New(context.Background(), cfg, testSystem(t, io.Discard)); err == nil {
		t.Fatal("New succeeded on a port in use")
	}
}

func TestBannerReportsDiskEncryption(t *testing.T) {
	for out, want := range map[string]string{
		"FileVault is Off.": "warning: disk encryption appears to be off",
		"FileVault is On.":  "",
		"":                  "could not tell whether disk encryption is on",
	} {
		sys := testSystem(t, &strings.Builder{})
		var banner strings.Builder
		sys.Out = &banner
		sys.DiskProbe = &diskcrypt.Probe{GOOS: "darwin", FileVault: func(context.Context) (string, error) {
			if out == "" {
				return "", errors.New("fdesetup not found")
			}
			return out, nil
		}}
		a, err := New(context.Background(), testConfig(t, true), sys)
		if err != nil {
			t.Fatal(err)
		}
		a.closeAll()
		got := banner.String()
		if want == "" && strings.Contains(got, "disk encryption") || want != "" && !strings.Contains(got, want) {
			t.Errorf("FileVault %q: banner\n%s", out, got)
		}
	}
}
