package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/garret/internal/datadir"
	"github.com/0xmhha/garret/internal/pki"
)

type wall struct{}

func (wall) Now() time.Time { return time.Now() }

var names = pki.Names{Host: "review.local", Networks: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}}

type kit struct {
	ca     *pki.CA
	certs  *pki.ServerCertificates
	active *pki.ActiveSerials
}

func newKit(t *testing.T) kit {
	t.Helper()
	d, err := datadir.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	ca, err := pki.LoadOrCreateCA(d, wall{}, names, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certs, err := pki.NewServerCertificates(ca, d, wall{}, names, slog.New(slog.DiscardHandler), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return kit{ca: ca, certs: certs, active: &pki.ActiveSerials{}}
}

// client issues a client certificate from ca (or a foreign CA).
func clientCert(t *testing.T, ca *pki.CA) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := pki.NewSerial(rand.Reader)
	tmpl, _ := pki.ClientTemplate(serial, time.Now(), "01JD000000000000000000BOB0")
	cert, err := pki.Sign(tmpl, ca.Cert, &key.PublicKey, ca.Key, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{cert.Raw}, PrivateKey: key, Leaf: cert}
}

func foreignCA(t *testing.T) *pki.CA {
	t.Helper()
	d, _ := datadir.Open(filepath.Join(t.TempDir(), "data"))
	ca, err := pki.LoadOrCreateCA(d, wall{}, names, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

// serve starts an HTTPS server on loopback with cfg and a handler that
// echoes the request identity.
func serve(t *testing.T, cfg *tls.Config, requireCert bool) string {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := IdentityOf(r, requireCert)
		if err != nil {
			http.Error(w, "no identity", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, id.Serial+" "+id.Remote.String())
	}))
	srv.TLS = cfg
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.URL
}

func get(t *testing.T, url string, ca *x509.Certificate, cert *tls.Certificate) (string, error) {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	cfg := &tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS13}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}, Timeout: 5 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", errors.New(resp.Status)
	}
	return string(body), nil
}

func TestMainTLSWithClientCertificates(t *testing.T) {
	k := newKit(t)
	good := clientCert(t, k.ca)
	revoked := clientCert(t, k.ca)
	k.active.Replace([]string{pki.SerialString(good.Leaf.SerialNumber)})
	url := serve(t, MainTLS(k.certs, k.ca.Cert, k.active, true), true)

	body, err := get(t, url, k.ca.Cert, &good)
	if err != nil {
		t.Fatalf("active certificate rejected: %v", err)
	}
	if !strings.HasPrefix(body, pki.SerialString(good.Leaf.SerialNumber)+" 127.0.0.1") {
		t.Fatalf("identity %q", body)
	}
	if _, err := get(t, url, k.ca.Cert, nil); err == nil {
		t.Fatal("handshake without a client certificate succeeded")
	}
	if _, err := get(t, url, k.ca.Cert, &revoked); err == nil {
		t.Fatal("certificate missing from the active set accepted")
	}
	stranger := clientCert(t, foreignCA(t))
	k.active.Replace([]string{pki.SerialString(good.Leaf.SerialNumber), pki.SerialString(stranger.Leaf.SerialNumber)})
	if _, err := get(t, url, k.ca.Cert, &stranger); err == nil {
		t.Fatal("certificate from another CA accepted even with its serial listed")
	}
}

func TestMainTLSWithoutClientCertificates(t *testing.T) {
	k := newKit(t)
	url := serve(t, MainTLS(k.certs, k.ca.Cert, k.active, false), false)
	body, err := get(t, url, k.ca.Cert, nil)
	if err != nil || !strings.HasPrefix(body, " 127.0.0.1") {
		t.Fatalf("certificates off: %q, %v", body, err)
	}
}

func TestMainTLSVersion(t *testing.T) {
	k := newKit(t)
	cfg := MainTLS(k.certs, k.ca.Cert, k.active, false)
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("min version %x", cfg.MinVersion)
	}
	if EnrollTLS(k.certs).ClientAuth != tls.NoClientCert {
		t.Fatal("enrollment listener asks for client certificates")
	}
}

func TestIdentityIgnoresHeaders(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	r.RemoteAddr = "[::ffff:192.168.1.9]:5000"
	r.Header.Set("X-Forwarded-For", "10.0.0.1")
	id, err := IdentityOf(r, false)
	if err != nil || id.Remote != netip.MustParseAddr("192.168.1.9") || id.Serial != "" {
		t.Fatalf("identity %+v, %v", id, err)
	}
	if _, err := IdentityOf(r, true); !errors.Is(err, ErrNoCertificate) {
		t.Fatalf("required certificate missing: %v", err)
	}
	r.RemoteAddr = "garbage"
	if _, err := IdentityOf(r, false); err == nil {
		t.Fatal("unparsable remote address accepted")
	}
}
