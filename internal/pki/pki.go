package pki

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/0xmhha/garret/internal/netguard"
)

var (
	// ErrSerial is returned when no non-zero serial could be read.
	ErrSerial = errors.New("pki: could not generate a serial number")
	// ErrNames is returned for template input that cannot produce a valid
	// certificate.
	ErrNames = errors.New("pki: invalid certificate names")
	// ErrKeyType is returned for a key that is not ECDSA P-256.
	ErrKeyType = errors.New("pki: key must be ECDSA P-256")
	// ErrCSR is returned for a certificate request that does not parse or
	// whose signature does not verify.
	ErrCSR = errors.New("pki: invalid certificate request")
)

const (
	serialBytes    = 16
	serialAttempts = 3
	backdate       = 5 * time.Minute
	caYears        = 10
	leafValidity   = 7 * 24 * time.Hour
	maxCSRBytes    = 4096
	maxHostBytes   = 253
	maxLabelBytes  = 63
	localhost      = "localhost"
	caCommonName   = "garret CA"
)

var loopbackRanges = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("::1/128"),
}

// Names are the host name and addresses the server is reached by.
type Names struct {
	Host      string
	Networks  []netip.Prefix // allowed networks; the CA is constrained to them
	Addresses []netip.Addr   // listen addresses, each inside Networks
}

// NewSerial returns a positive serial number of at most 127 bits, read from
// rand, within the 20-octet limit of RFC 5280 section 4.1.2.2.
func NewSerial(rand io.Reader) (*big.Int, error) {
	b := make([]byte, serialBytes)
	for range serialAttempts {
		if _, err := io.ReadFull(rand, b); err != nil {
			return nil, fmt.Errorf("pki: read serial: %w", err)
		}
		b[0] &= 0x7f // keep the integer positive in DER
		if s := new(big.Int).SetBytes(b); s.Sign() > 0 {
			return s, nil
		}
	}
	return nil, ErrSerial
}

// Fingerprint returns the SHA-256 of the certificate's DER encoding as 64
// lowercase hex characters, or "" for a nil certificate.
func Fingerprint(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// CATemplate returns the template of garret's CA: valid for 10 years from
// five minutes before now, allowed to sign leaf certificates only, and
// constrained to names.Host, localhost, names.Networks and loopback.
func CATemplate(serial *big.Int, now time.Time, names Names) (*x509.Certificate, error) {
	if err := names.check(); err != nil {
		return nil, err
	}
	if serial == nil {
		return nil, fmt.Errorf("%w: nil serial", ErrNames)
	}
	permitted := make([]*net.IPNet, 0, len(names.Networks)+len(loopbackRanges))
	for _, p := range append(append([]netip.Prefix(nil), names.Networks...), loopbackRanges...) {
		permitted = append(permitted, ipNet(p))
	}
	return &x509.Certificate{
		SerialNumber:                serial,
		Subject:                     pkix.Name{CommonName: caCommonName},
		NotBefore:                   now.Add(-backdate),
		NotAfter:                    now.AddDate(caYears, 0, 0),
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid:       true,
		IsCA:                        true,
		MaxPathLen:                  0,
		MaxPathLenZero:              true,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{names.Host, localhost},
		PermittedIPRanges:           permitted,
	}, nil
}

// ServerTemplate returns the template of a server certificate valid for 7
// days from five minutes before now, for names.Host, localhost, each listen
// address and loopback.
func ServerTemplate(serial *big.Int, now time.Time, names Names) (*x509.Certificate, error) {
	if err := names.check(); err != nil {
		return nil, err
	}
	if serial == nil {
		return nil, fmt.Errorf("%w: nil serial", ErrNames)
	}
	ips := make([]net.IP, 0, len(names.Addresses)+len(loopbackRanges))
	for _, a := range names.Addresses {
		ips = append(ips, net.IP(a.AsSlice()))
	}
	ips = append(ips, net.IPv4(127, 0, 0, 1), net.IPv6loopback)
	return leaf(serial, now, names.Host, x509.ExtKeyUsageServerAuth, []string{names.Host, localhost}, ips), nil
}

// ClientTemplate returns the template of a client certificate for userID,
// valid for 7 days from five minutes before now. It carries no SANs; the
// server identifies the device by its serial.
func ClientTemplate(serial *big.Int, now time.Time, userID string) (*x509.Certificate, error) {
	if serial == nil || userID == "" {
		return nil, fmt.Errorf("%w: nil serial or empty user ID", ErrNames)
	}
	return leaf(serial, now, userID, x509.ExtKeyUsageClientAuth, nil, nil), nil
}

func leaf(serial *big.Int, now time.Time, cn string, usage x509.ExtKeyUsage, dns []string, ips []net.IP) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.Add(leafValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{usage},
		BasicConstraintsValid: true,
		DNSNames:              dns,
		IPAddresses:           ips,
	}
}

// Sign issues template for pub, signed by signer under parent, and returns
// the parsed certificate. For a self-signed certificate parent is template.
// It returns ErrKeyType unless pub is an ECDSA P-256 key.
func Sign(template, parent *x509.Certificate, pub crypto.PublicKey, signer crypto.Signer, rand io.Reader) (*x509.Certificate, error) {
	if !isP256(pub) {
		return nil, ErrKeyType
	}
	der, err := x509.CreateCertificate(rand, template, parent, pub, signer)
	if err != nil {
		return nil, fmt.Errorf("pki: sign certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("pki: parse issued certificate: %w", err)
	}
	return cert, nil
}

// CheckCSR parses a DER certificate request, verifies its signature and
// requires an ECDSA P-256 key. Subject and extensions in the request are not
// used. It returns ErrCSR or ErrKeyType.
func CheckCSR(der []byte) (*x509.CertificateRequest, error) {
	if len(der) == 0 || len(der) > maxCSRBytes {
		return nil, ErrCSR
	}
	req, err := x509.ParseCertificateRequest(der)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCSR, err)
	}
	if !isP256(req.PublicKey) {
		return nil, ErrKeyType
	}
	if err := req.CheckSignature(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCSR, err)
	}
	return req, nil
}

// RenewalDue reports whether less than half of the validity notBefore to
// notAfter remains at now. A certificate whose notAfter is not after its
// notBefore is always due.
func RenewalDue(notBefore, notAfter, now time.Time) bool {
	if !notAfter.After(notBefore) {
		return true
	}
	half := notAfter.Sub(notBefore) / 2
	return !now.Before(notBefore.Add(half))
}

func isP256(pub crypto.PublicKey) bool {
	k, ok := pub.(*ecdsa.PublicKey)
	return ok && k.Curve == elliptic.P256()
}

func (n Names) check() error {
	if !validHost(n.Host) {
		return fmt.Errorf("%w: host %q", ErrNames, n.Host)
	}
	if len(n.Networks) == 0 {
		return fmt.Errorf("%w: no networks", ErrNames)
	}
	for _, p := range n.Networks {
		if !netguard.IsPrivate(p) {
			return fmt.Errorf("%w: network %s is not private", ErrNames, p)
		}
	}
	for _, a := range n.Addresses {
		if !inAny(a, n.Networks) {
			return fmt.Errorf("%w: address %s is outside the networks", ErrNames, a)
		}
	}
	return nil
}

func inAny(a netip.Addr, prefixes []netip.Prefix) bool {
	a = a.Unmap()
	for _, p := range prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// validHost accepts a DNS host name: dot-separated labels of letters, digits
// and inner hyphens, each 1-63 bytes, 253 bytes in total.
func validHost(h string) bool {
	if h == "" || len(h) > maxHostBytes {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > maxLabelBytes || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := range len(label) {
			if !hostByte(label[i]) {
				return false
			}
		}
	}
	return true
}

func hostByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-'
}

func ipNet(p netip.Prefix) *net.IPNet {
	p = p.Masked()
	bits := p.Addr().BitLen()
	return &net.IPNet{IP: net.IP(p.Addr().AsSlice()), Mask: net.CIDRMask(p.Bits(), bits)}
}
