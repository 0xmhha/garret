package pki

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math/big"
	"net/netip"
	"slices"
	"sync/atomic"
	"time"

	"github.com/0xmhha/garret/internal/clock"
	"github.com/0xmhha/garret/internal/datadir"
)

var (
	// ErrNoCA is returned by LoadCA when the data directory has no CA yet.
	ErrNoCA = errors.New("pki: no CA in the data directory; start garret serve once first")
	// ErrCAIncomplete is returned when only one of the CA key and
	// certificate exists.
	ErrCAIncomplete = errors.New("pki: CA key or certificate is missing")
	// ErrCAMismatch is returned when the stored CA's name constraints do not
	// cover the server's current names, so members must enroll again.
	ErrCAMismatch = errors.New("pki: stored CA does not cover the current host and network; re-enrollment is required")
)

const (
	caKeyFile      = "tls/ca.key"
	caCertFile     = "tls/ca.crt"
	serverKeyFile  = "tls/server.key"
	serverCertFile = "tls/server.crt"
	pemCertificate = "CERTIFICATE"
	pemPrivateKey  = "PRIVATE KEY"
)

// CA is garret's certificate authority.
type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
}

// LoadCA loads the CA without creating one, for commands that run beside
// the server. It returns ErrNoCA when neither file exists and
// ErrCAIncomplete when only one does.
func LoadCA(dir *datadir.Dir) (*CA, error) {
	keyPEM, keyErr := dir.ReadFile(caKeyFile)
	certPEM, certErr := dir.ReadFile(caCertFile)
	keyMissing, certMissing := errors.Is(keyErr, fs.ErrNotExist), errors.Is(certErr, fs.ErrNotExist)
	switch {
	case keyMissing && certMissing:
		return nil, ErrNoCA
	case keyMissing || certMissing:
		return nil, ErrCAIncomplete
	case keyErr != nil:
		return nil, keyErr
	case certErr != nil:
		return nil, certErr
	}
	key, cert, err := parsePair(keyPEM, certPEM)
	if err != nil {
		return nil, fmt.Errorf("pki: load CA: %w", err)
	}
	if !cert.IsCA {
		return nil, ErrCAMismatch
	}
	return &CA{Cert: cert, Key: key}, nil
}

// LoadOrCreateCA loads the CA from the data directory, or creates it when
// neither file exists. It returns ErrCAIncomplete when only one file exists
// and ErrCAMismatch when the stored CA cannot sign for names.
func LoadOrCreateCA(dir *datadir.Dir, c clock.Clock, names Names, rand io.Reader) (*CA, error) {
	keyPEM, keyErr := dir.ReadFile(caKeyFile)
	certPEM, certErr := dir.ReadFile(caCertFile)
	keyMissing, certMissing := errors.Is(keyErr, fs.ErrNotExist), errors.Is(certErr, fs.ErrNotExist)
	switch {
	case keyMissing && certMissing:
		return createCA(dir, c.Now(), names, rand)
	case keyMissing || certMissing:
		return nil, ErrCAIncomplete
	case keyErr != nil:
		return nil, keyErr
	case certErr != nil:
		return nil, certErr
	}
	key, cert, err := parsePair(keyPEM, certPEM)
	if err != nil {
		return nil, fmt.Errorf("pki: load CA: %w", err)
	}
	if !cert.IsCA || !covers(cert, names) {
		return nil, ErrCAMismatch
	}
	return &CA{Cert: cert, Key: key}, nil
}

func createCA(dir *datadir.Dir, now time.Time, names Names, rand io.Reader) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand)
	if err != nil {
		return nil, fmt.Errorf("pki: generate CA key: %w", err)
	}
	serial, err := NewSerial(rand)
	if err != nil {
		return nil, err
	}
	tmpl, err := CATemplate(serial, now, names)
	if err != nil {
		return nil, err
	}
	cert, err := Sign(tmpl, tmpl, &key.PublicKey, key, rand)
	if err != nil {
		return nil, err
	}
	if err := writePair(dir, caKeyFile, caCertFile, key, cert); err != nil {
		return nil, err
	}
	return &CA{Cert: cert, Key: key}, nil
}

// covers reports whether the CA's name constraints permit every name the
// server will put in its certificate.
func covers(ca *x509.Certificate, names Names) bool {
	if names.check() != nil || !slices.Contains(ca.PermittedDNSDomains, names.Host) {
		return false
	}
	var permitted []netip.Prefix
	for _, n := range ca.PermittedIPRanges {
		addr, ok := netip.AddrFromSlice(n.IP)
		ones, _ := n.Mask.Size()
		if ok {
			permitted = append(permitted, netip.PrefixFrom(addr.Unmap(), ones))
		}
	}
	for _, p := range names.Networks {
		inside := slices.ContainsFunc(permitted, func(r netip.Prefix) bool {
			return r.Addr().BitLen() == p.Addr().BitLen() && p.Bits() >= r.Bits() && r.Contains(p.Addr())
		})
		if !inside {
			return false
		}
	}
	return true
}

// ServerCertificates holds the current server certificate and replaces it
// before it expires. GetCertificate is safe for concurrent use.
type ServerCertificates struct {
	ca    *CA
	dir   *datadir.Dir
	clock clock.Clock
	names Names
	log   *slog.Logger
	rand  io.Reader
	cur   atomic.Pointer[tls.Certificate]
}

// NewServerCertificates loads the stored server certificate if it is signed
// by ca, covers names and is not yet due for renewal; otherwise it issues a
// new one.
func NewServerCertificates(ca *CA, dir *datadir.Dir, c clock.Clock, names Names, log *slog.Logger, rand io.Reader) (*ServerCertificates, error) {
	s := &ServerCertificates{ca: ca, dir: dir, clock: c, names: names, log: log, rand: rand}
	if tc, ok := s.loadStored(); ok {
		s.cur.Store(tc)
		return s, nil
	}
	if err := s.issue(); err != nil {
		return nil, err
	}
	return s, nil
}

// GetCertificate returns the current certificate, for tls.Config.
func (s *ServerCertificates) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return s.cur.Load(), nil
}

// Run checks every interval whether the certificate is due and replaces it.
// A failed renewal keeps the current certificate and is retried at the next
// check. Run returns when ctx is done.
func (s *ServerCertificates) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.rotateIfDue(); err != nil {
				s.log.Error("server certificate renewal failed; keeping the current one", "err", err)
			}
		}
	}
}

func (s *ServerCertificates) rotateIfDue() error {
	leaf := s.cur.Load().Leaf
	if !RenewalDue(leaf.NotBefore, leaf.NotAfter, s.clock.Now()) {
		return nil
	}
	return s.issue()
}

func (s *ServerCertificates) issue() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), s.rand)
	if err != nil {
		return fmt.Errorf("pki: generate server key: %w", err)
	}
	serial, err := NewSerial(s.rand)
	if err != nil {
		return err
	}
	tmpl, err := ServerTemplate(serial, s.clock.Now(), s.names)
	if err != nil {
		return err
	}
	cert, err := Sign(tmpl, s.ca.Cert, &key.PublicKey, s.ca.Key, s.rand)
	if err != nil {
		return err
	}
	if err := writePair(s.dir, serverKeyFile, serverCertFile, key, cert); err != nil {
		return err
	}
	s.cur.Store(tlsCertificate(key, cert))
	return nil
}

func (s *ServerCertificates) loadStored() (*tls.Certificate, bool) {
	keyPEM, err1 := s.dir.ReadFile(serverKeyFile)
	certPEM, err2 := s.dir.ReadFile(serverCertFile)
	if err1 != nil || err2 != nil {
		return nil, false
	}
	key, cert, err := parsePair(keyPEM, certPEM)
	if err != nil {
		return nil, false
	}
	roots := x509.NewCertPool()
	roots.AddCert(s.ca.Cert)
	now := s.clock.Now()
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: s.names.Host, CurrentTime: now,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return nil, false
	}
	for _, a := range s.names.Addresses {
		if cert.VerifyHostname(a.String()) != nil {
			return nil, false
		}
	}
	if RenewalDue(cert.NotBefore, cert.NotAfter, now) {
		return nil, false
	}
	return tlsCertificate(key, cert), true
}

func tlsCertificate(key *ecdsa.PrivateKey, cert *x509.Certificate) *tls.Certificate {
	return &tls.Certificate{Certificate: [][]byte{cert.Raw}, PrivateKey: key, Leaf: cert}
}

// writePair writes the key first: a crash between the two writes leaves a
// pair that fails to parse together, which is then reissued.
func writePair(dir *datadir.Dir, keyFile, certFile string, key *ecdsa.PrivateKey, cert *x509.Certificate) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("pki: encode key: %w", err)
	}
	if err := dir.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: pemPrivateKey, Bytes: der})); err != nil {
		return err
	}
	return dir.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: pemCertificate, Bytes: cert.Raw}))
}

func parsePair(keyPEM, certPEM []byte) (*ecdsa.PrivateKey, *x509.Certificate, error) {
	kb, _ := pem.Decode(keyPEM)
	cb, _ := pem.Decode(certPEM)
	if kb == nil || kb.Type != pemPrivateKey || cb == nil || cb.Type != pemCertificate {
		return nil, nil, errors.New("pki: malformed PEM")
	}
	k, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("pki: parse key: %w", err)
	}
	key, ok := k.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, nil, ErrKeyType
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("pki: parse certificate: %w", err)
	}
	if !key.PublicKey.Equal(cert.PublicKey) {
		return nil, nil, errors.New("pki: key does not match certificate")
	}
	return key, cert, nil
}

// ActiveSerials is the set of client certificate serials that may complete
// a handshake (docs/specs/data.md section 7.4). Reads never lock; Replace
// publishes a new set. The zero value is an empty set.
type ActiveSerials struct {
	set atomic.Pointer[map[string]struct{}]
}

// Replace makes serials the active set.
func (a *ActiveSerials) Replace(serials []string) {
	m := make(map[string]struct{}, len(serials))
	for _, s := range serials {
		m[s] = struct{}{}
	}
	a.set.Store(&m)
}

// Active reports whether serial is in the current set.
func (a *ActiveSerials) Active(serial string) bool {
	m := a.set.Load()
	if m == nil {
		return false
	}
	_, ok := (*m)[serial]
	return ok
}

// SerialString formats a certificate serial the way client_certificates
// stores it: lowercase hex without leading zeros.
func SerialString(n *big.Int) string {
	return n.Text(16)
}
