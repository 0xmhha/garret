package pki

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"math/big"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/garret/internal/datadir"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func openDir(t *testing.T) *datadir.Dir {
	t.Helper()
	d, err := datadir.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestLoadOrCreateCA(t *testing.T) {
	d := openDir(t)
	c := &fakeClock{now: now}
	ca, err := LoadOrCreateCA(d, c, names, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"ca.key", "ca.crt"} {
		fi, err := os.Stat(filepath.Join(d.TLSDir(), f))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v mode %v", f, err, fi.Mode())
		}
	}
	again, err := LoadOrCreateCA(d, c, names, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(again.Cert) != Fingerprint(ca.Cert) {
		t.Fatal("second call created a new CA instead of loading")
	}
	if !again.Key.PublicKey.Equal(ca.Cert.PublicKey) {
		t.Fatal("loaded key does not match the certificate")
	}
}

func TestLoadOrCreateCAIncomplete(t *testing.T) {
	d := openDir(t)
	if _, err := LoadOrCreateCA(d, &fakeClock{now: now}, names, rand.Reader); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(d.TLSDir(), "ca.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateCA(d, &fakeClock{now: now}, names, rand.Reader); !errors.Is(err, ErrCAIncomplete) {
		t.Fatalf("err = %v, want ErrCAIncomplete", err)
	}
}

func TestLoadOrCreateCAMismatch(t *testing.T) {
	d := openDir(t)
	if _, err := LoadOrCreateCA(d, &fakeClock{now: now}, names, rand.Reader); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		names Names
	}{
		{"other host", Names{Host: "other.local", Networks: names.Networks}},
		{"other network", Names{Host: names.Host, Networks: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}}},
		{"wider network", Names{Host: names.Host, Networks: []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := LoadOrCreateCA(d, &fakeClock{now: now}, tt.names, rand.Reader); !errors.Is(err, ErrCAMismatch) {
				t.Fatalf("err = %v, want ErrCAMismatch", err)
			}
		})
	}
	narrower := Names{Host: names.Host, Networks: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/25")},
		Addresses: []netip.Addr{netip.MustParseAddr("192.168.1.23")}}
	if _, err := LoadOrCreateCA(d, &fakeClock{now: now}, narrower, rand.Reader); err != nil {
		t.Fatalf("a narrower network inside the CA's should load: %v", err)
	}
}

func TestLoadOrCreateCACorrupt(t *testing.T) {
	d := openDir(t)
	if _, err := LoadOrCreateCA(d, &fakeClock{now: now}, names, rand.Reader); err != nil {
		t.Fatal(err)
	}
	if err := d.WriteFile("tls/ca.crt", []byte("not pem")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateCA(d, &fakeClock{now: now}, names, rand.Reader); err == nil {
		t.Fatal("corrupt certificate loaded")
	}
}

func TestLoadOrCreateCAKeyMismatch(t *testing.T) {
	a, b := openDir(t), openDir(t)
	if _, err := LoadOrCreateCA(a, &fakeClock{now: now}, names, rand.Reader); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateCA(b, &fakeClock{now: now}, names, rand.Reader); err != nil {
		t.Fatal(err)
	}
	other, err := b.ReadFile("tls/ca.key")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.WriteFile("tls/ca.key", other); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateCA(a, &fakeClock{now: now}, names, rand.Reader); err == nil {
		t.Fatal("a key that does not match the certificate was accepted")
	}
}

func newServerCerts(t *testing.T) (*ServerCertificates, *CA, *datadir.Dir, *fakeClock) {
	t.Helper()
	d := openDir(t)
	c := &fakeClock{now: now}
	ca, err := LoadOrCreateCA(d, c, names, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServerCertificates(ca, d, c, names, slog.New(slog.DiscardHandler), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return s, ca, d, c
}

func current(t *testing.T, s *ServerCertificates) *x509.Certificate {
	t.Helper()
	tc, err := s.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatal(err)
	}
	return tc.Leaf
}

func TestServerCertificatesIssueAndReuse(t *testing.T) {
	s, ca, d, c := newServerCerts(t)
	first := current(t, s)
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	if _, err := first.Verify(x509.VerifyOptions{Roots: roots, DNSName: names.Host, CurrentTime: c.Now()}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewServerCertificates(ca, d, c, names, slog.New(slog.DiscardHandler), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if current(t, reloaded).SerialNumber.Cmp(first.SerialNumber) != 0 {
		t.Fatal("a valid stored certificate was not reused")
	}
}

func TestServerCertificatesRotate(t *testing.T) {
	s, _, _, c := newServerCerts(t)
	first := current(t, s)
	c.advance(72 * time.Hour)
	if err := s.rotateIfDue(); err != nil {
		t.Fatal(err)
	}
	if current(t, s).SerialNumber.Cmp(first.SerialNumber) != 0 {
		t.Fatal("rotated before half of the validity")
	}
	c.advance(13 * time.Hour)
	if err := s.rotateIfDue(); err != nil {
		t.Fatal(err)
	}
	second := current(t, s)
	if second.SerialNumber.Cmp(first.SerialNumber) == 0 {
		t.Fatal("not rotated after half of the validity")
	}
	if !second.NotBefore.Equal(c.Now().Add(-backdate)) {
		t.Fatalf("new certificate starts at %v", second.NotBefore)
	}
}

func TestServerCertificatesRotationFailureKeepsOld(t *testing.T) {
	s, _, d, c := newServerCerts(t)
	first := current(t, s)
	c.advance(100 * time.Hour)
	if err := os.Chmod(d.TLSDir(), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(d.TLSDir(), 0o700) })
	if err := s.rotateIfDue(); err == nil {
		t.Fatal("rotation into a read-only directory succeeded")
	}
	if current(t, s).SerialNumber.Cmp(first.SerialNumber) != 0 {
		t.Fatal("failed rotation replaced the certificate")
	}
}

func TestServerCertificatesRunStops(t *testing.T) {
	s, _, _, _ := newServerCerts(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx, time.Millisecond)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestActiveSerials(t *testing.T) {
	var a ActiveSerials
	if a.Active("01") {
		t.Fatal("empty set reports a serial active")
	}
	a.Replace([]string{"0a", "0b"})
	if !a.Active("0a") || !a.Active("0b") || a.Active("0c") {
		t.Fatal("membership wrong after Replace")
	}
	a.Replace([]string{"0c"})
	if a.Active("0a") || !a.Active("0c") {
		t.Fatal("old set still visible after Replace")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				_ = a.Active("0c")
				a.Replace([]string{"0c"})
			}
		})
	}
	wg.Wait()
}

func TestSerialString(t *testing.T) {
	if got := SerialString(big.NewInt(255)); got != "ff" {
		t.Fatalf("SerialString(255) = %q", got)
	}
}

func TestLoadCA(t *testing.T) {
	d := openDir(t)
	if _, err := LoadCA(d); !errors.Is(err, ErrNoCA) {
		t.Fatalf("empty directory: %v", err)
	}
	ca, err := LoadOrCreateCA(d, &fakeClock{now: now}, names, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadCA(d)
	if err != nil || Fingerprint(got.Cert) != Fingerprint(ca.Cert) || !got.Key.Equal(ca.Key) {
		t.Fatalf("LoadCA = %v", err)
	}
	if err := os.Remove(filepath.Join(d.TLSDir(), "ca.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCA(d); !errors.Is(err, ErrCAIncomplete) {
		t.Fatalf("missing key: %v", err)
	}
}
