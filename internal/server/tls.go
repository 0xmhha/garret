package server

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/netip"

	"github.com/0xmhha/garret/internal/pki"
)

var (
	// ErrNoCertificate is returned when client certificates are required and
	// the request has no verified chain.
	ErrNoCertificate = errors.New("server: no verified client certificate")
	// errInactive is returned from the handshake for a certificate that is
	// not in the active set.
	errInactive = errors.New("server: client certificate is not active")
)

// MainTLS returns the TLS configuration of the main listener. With
// clientCerts, the handshake requires a certificate that chains to ca alone
// and whose serial is in active (transport.md 2.1).
func MainTLS(certs *pki.ServerCertificates, ca *x509.Certificate, active *pki.ActiveSerials, clientCerts bool) *tls.Config {
	cfg := &tls.Config{
		MinVersion:     tls.VersionTLS13,
		GetCertificate: certs.GetCertificate,
		ClientAuth:     tls.NoClientCert,
	}
	if !clientCerts {
		return cfg
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	cfg.ClientCAs = pool
	cfg.ClientAuth = tls.RequireAndVerifyClientCert
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.VerifiedChains) == 0 || len(cs.VerifiedChains[0]) == 0 {
			return ErrNoCertificate
		}
		if !active.Active(pki.SerialString(cs.VerifiedChains[0][0].SerialNumber)) {
			return errInactive
		}
		return nil
	}
	return cfg
}

// EnrollTLS returns the TLS configuration of the enrollment listener, which
// accepts devices that have no certificate yet.
func EnrollTLS(certs *pki.ServerCertificates) *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: certs.GetCertificate, ClientAuth: tls.NoClientCert}
}

// Identity is who a request comes from at the transport level.
type Identity struct {
	Serial string     // verified client certificate serial, "" when certificates are off
	Remote netip.Addr // TCP peer address, unmapped
}

// IdentityOf derives the identity from the TLS state and the socket address
// only; headers such as X-Forwarded-For are never consulted. With
// requireCert, a request without a verified chain returns ErrNoCertificate.
func IdentityOf(r *http.Request, requireCert bool) (Identity, error) {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return Identity{}, fmt.Errorf("server: remote address %q: %w", r.RemoteAddr, err)
	}
	id := Identity{Remote: ap.Addr().Unmap()}
	if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 && len(r.TLS.VerifiedChains[0]) > 0 {
		id.Serial = pki.SerialString(r.TLS.VerifiedChains[0][0].SerialNumber)
	}
	if requireCert && id.Serial == "" {
		return Identity{}, ErrNoCertificate
	}
	return id, nil
}
