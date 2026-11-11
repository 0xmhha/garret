package pki

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/netip"
	"os/exec"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

var names = Names{
	Host:      "review-host.local",
	Networks:  []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")},
	Addresses: []netip.Addr{netip.MustParseAddr("192.168.1.23")},
}

func newKey(t *testing.T, c elliptic.Curve) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(c, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func serial(t *testing.T) *big.Int {
	t.Helper()
	s, err := NewSerial(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// testCA returns a self-signed CA certificate and its key.
func testCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key := newKey(t, elliptic.P256())
	tmpl, err := CATemplate(serial(t), now, names)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := Sign(tmpl, tmpl, &key.PublicKey, key, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return ca, key
}

func TestNewSerial(t *testing.T) {
	zeros := bytes.NewReader(make([]byte, 16*serialAttempts))
	tests := []struct {
		name    string
		r       interface{ Read([]byte) (int, error) }
		wantErr bool
	}{
		{"random", rand.Reader, false},
		{"zeros every time", zeros, true},
		{"zeros once then data", &concatReader{parts: [][]byte{make([]byte, 16), bytes.Repeat([]byte{0xff}, 16)}}, false},
		{"short read", bytes.NewReader(make([]byte, 15)), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := NewSerial(tt.r)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NewSerial = %v, want an error", s)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.Sign() <= 0 || s.BitLen() > 127 {
				t.Fatalf("serial %v: sign %d, bit length %d", s, s.Sign(), s.BitLen())
			}
		})
	}
}

type concatReader struct{ parts [][]byte }

func (r *concatReader) Read(p []byte) (int, error) {
	if len(r.parts) == 0 {
		return 0, errors.New("exhausted")
	}
	n := copy(p, r.parts[0])
	r.parts[0] = r.parts[0][n:]
	if len(r.parts[0]) == 0 {
		r.parts = r.parts[1:]
	}
	return n, nil
}

func TestFingerprint(t *testing.T) {
	ca, _ := testCA(t)
	fp := Fingerprint(ca)
	if len(fp) != 64 || strings.ToLower(fp) != fp {
		t.Fatalf("fingerprint %q is not 64 lowercase hex characters", fp)
	}
	if Fingerprint(ca) != fp {
		t.Fatal("fingerprint changed between calls")
	}
	if Fingerprint(nil) != "" {
		t.Fatal("nil certificate should give an empty fingerprint")
	}
	openssl, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("openssl not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, openssl, "x509", "-inform", "DER", "-noout", "-fingerprint", "-sha256")
	cmd.Stdin = bytes.NewReader(ca.Raw)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	_, hexColons, _ := strings.Cut(strings.TrimSpace(string(out)), "=")
	if want := strings.ToLower(strings.ReplaceAll(hexColons, ":", "")); fp != want {
		t.Fatalf("fingerprint %s, openssl says %s", fp, want)
	}
}

func TestCATemplate(t *testing.T) {
	ca, _ := testCA(t)
	if !ca.IsCA || ca.MaxPathLen != 0 || !ca.MaxPathLenZero {
		t.Fatalf("CA flags: IsCA=%v MaxPathLen=%d zero=%v", ca.IsCA, ca.MaxPathLen, ca.MaxPathLenZero)
	}
	if !ca.NotBefore.Equal(now.Add(-backdate)) || !ca.NotAfter.Equal(now.AddDate(caYears, 0, 0)) {
		t.Fatalf("validity %v to %v", ca.NotBefore, ca.NotAfter)
	}
	if ca.KeyUsage != x509.KeyUsageCertSign|x509.KeyUsageCRLSign {
		t.Fatalf("key usage %v", ca.KeyUsage)
	}
	if !ca.PermittedDNSDomainsCritical {
		t.Fatal("name constraints are not critical")
	}
	if got := strings.Join(ca.PermittedDNSDomains, ","); got != "review-host.local,localhost" {
		t.Fatalf("permitted DNS %s", got)
	}
	var ranges []string
	for _, r := range ca.PermittedIPRanges {
		ranges = append(ranges, r.String())
	}
	if got := strings.Join(ranges, ","); got != "192.168.1.0/24,127.0.0.0/8,::1/128" {
		t.Fatalf("permitted IP ranges %s", got)
	}
}

func TestServerCertificateVerifies(t *testing.T) {
	ca, caKey := testCA(t)
	key := newKey(t, elliptic.P256())
	tmpl, err := ServerTemplate(serial(t), now, names)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := Sign(tmpl, ca, &key.PublicKey, caKey, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if !cert.NotBefore.Equal(now.Add(-backdate)) || !cert.NotAfter.Equal(now.Add(leafValidity)) {
		t.Fatalf("validity %v to %v", cert.NotBefore, cert.NotAfter)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	for _, name := range []string{"review-host.local", "localhost", "192.168.1.23", "127.0.0.1", "::1"} {
		_, err := cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: name, CurrentTime: now.Add(time.Hour),
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
		if err != nil {
			t.Errorf("verify for %s: %v", name, err)
		}
	}
}

func TestNameConstraintsRejectOtherNames(t *testing.T) {
	ca, caKey := testCA(t)
	key := newKey(t, elliptic.P256())
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	tests := []struct {
		name string
		tmpl *x509.Certificate
	}{
		{"other DNS name", &x509.Certificate{SerialNumber: serial(t), DNSNames: []string{"evil.example"}}},
		{"other IP address", &x509.Certificate{SerialNumber: serial(t), IPAddresses: []net.IP{net.ParseIP("10.0.0.1")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.tmpl.NotBefore, tt.tmpl.NotAfter = now, now.Add(leafValidity)
			tt.tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			cert, err := Sign(tt.tmpl, ca, &key.PublicKey, caKey, rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			_, err = cert.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now.Add(time.Hour),
				KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
			var invalid x509.CertificateInvalidError
			if !errors.As(err, &invalid) || invalid.Reason != x509.CANotAuthorizedForThisName {
				t.Fatalf("err = %v, want CANotAuthorizedForThisName", err)
			}
		})
	}
}

func TestClientCertificateVerifies(t *testing.T) {
	ca, caKey := testCA(t)
	key := newKey(t, elliptic.P256())
	tmpl, err := ClientTemplate(serial(t), now, "01JD0000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := Sign(tmpl, ca, &key.PublicKey, caKey, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if cert.Subject.CommonName != "01JD0000000000000000000000" || len(cert.DNSNames)+len(cert.IPAddresses) != 0 {
		t.Fatalf("subject %v, SANs %v %v", cert.Subject, cert.DNSNames, cert.IPAddresses)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now.Add(time.Hour),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatal(err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now.Add(time.Hour),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err == nil {
		t.Fatal("client certificate verified for server use")
	}
}

func TestTemplateErrors(t *testing.T) {
	bad := func(f func(*Names)) Names {
		n := names
		n.Networks = append([]netip.Prefix(nil), names.Networks...)
		n.Addresses = append([]netip.Addr(nil), names.Addresses...)
		f(&n)
		return n
	}
	tests := []struct {
		name  string
		names Names
	}{
		{"empty host", bad(func(n *Names) { n.Host = "" })},
		{"host with space", bad(func(n *Names) { n.Host = "bad host" })},
		{"host with leading dash", bad(func(n *Names) { n.Host = "-host" })},
		{"host label over 63", bad(func(n *Names) { n.Host = strings.Repeat("a", 64) })},
		{"no networks", bad(func(n *Names) { n.Networks = nil })},
		{"public network", bad(func(n *Names) { n.Networks = append(n.Networks, netip.MustParsePrefix("8.8.8.0/24")) })},
		{"address outside networks", bad(func(n *Names) { n.Addresses = []netip.Addr{netip.MustParseAddr("10.0.0.1")} })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := CATemplate(serial(t), now, tt.names); !errors.Is(err, ErrNames) {
				t.Errorf("CATemplate err = %v, want ErrNames", err)
			}
			if _, err := ServerTemplate(serial(t), now, tt.names); !errors.Is(err, ErrNames) {
				t.Errorf("ServerTemplate err = %v, want ErrNames", err)
			}
		})
	}
	if _, err := CATemplate(nil, now, names); !errors.Is(err, ErrNames) {
		t.Errorf("nil serial: err = %v", err)
	}
	if _, err := ClientTemplate(serial(t), now, ""); !errors.Is(err, ErrNames) {
		t.Errorf("empty user ID: err = %v", err)
	}
	if _, err := ClientTemplate(nil, now, "u"); !errors.Is(err, ErrNames) {
		t.Errorf("nil serial: err = %v", err)
	}
}

func TestSignRejectsOtherKeyTypes(t *testing.T) {
	ca, caKey := testCA(t)
	tmpl, err := ClientTemplate(serial(t), now, "u")
	if err != nil {
		t.Fatal(err)
	}
	p384 := newKey(t, elliptic.P384())
	if _, err := Sign(tmpl, ca, &p384.PublicKey, caKey, rand.Reader); !errors.Is(err, ErrKeyType) {
		t.Fatalf("err = %v, want ErrKeyType", err)
	}
}

func csr(t *testing.T, key any) []byte {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "ignored"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestCheckCSR(t *testing.T) {
	good := csr(t, newKey(t, elliptic.P256()))
	tampered := bytes.Clone(good)
	tampered[len(tampered)-5] ^= 0xff
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		der  []byte
		want error
	}{
		{"valid P-256", good, nil},
		{"P-384", csr(t, newKey(t, elliptic.P384())), ErrKeyType},
		{"RSA", csr(t, rsaKey), ErrKeyType},
		{"tampered signature", tampered, ErrCSR},
		{"empty", nil, ErrCSR},
		{"garbage", []byte("not a request"), ErrCSR},
		{"over 4 KiB", append(bytes.Clone(good), make([]byte, maxCSRBytes)...), ErrCSR},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := CheckCSR(tt.der)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if err == nil && req == nil {
				t.Fatal("nil request without an error")
			}
		})
	}
}

func TestRenewalDue(t *testing.T) {
	nb := now
	na := now.Add(leafValidity)
	tests := []struct {
		name       string
		nb, na, at time.Time
		want       bool
	}{
		{"3 days in", nb, na, nb.Add(72 * time.Hour), false},
		{"exactly half", nb, na, nb.Add(84 * time.Hour), true},
		{"just before half", nb, na, nb.Add(84*time.Hour - time.Nanosecond), false},
		{"after expiry", nb, na, na.Add(time.Hour), true},
		{"before not-before", nb, na, nb.Add(-time.Hour), false},
		{"not-after equals not-before", nb, nb, nb, true},
		{"not-after before not-before", na, nb, nb, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RenewalDue(tt.nb, tt.na, tt.at); got != tt.want {
				t.Fatalf("RenewalDue = %v, want %v", got, tt.want)
			}
		})
	}
}
