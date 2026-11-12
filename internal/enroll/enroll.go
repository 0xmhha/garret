package enroll

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

var (
	// ErrLink is returned for an invitation link of the wrong form.
	ErrLink = errors.New("enroll: not a garret invitation link")
	// ErrFingerprint is returned when the server's CA does not match the
	// link: the connection may be intercepted.
	ErrFingerprint = errors.New("enroll: the server's CA does not match the invitation link")
	// ErrRejected is returned when the server refuses the request; the
	// message is the server's.
	ErrRejected = errors.New("enroll: the server refused the request")
)

const (
	requestTimeout = 30 * time.Second
	maxResponse    = 1 << 20
	maxCABytes     = 64 << 10
)

var fingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Link is a parsed invitation link. It holds no secret.
type Link struct {
	Host          string
	Port          uint16
	CAFingerprint string
}

// Base returns the enrollment listener's URL.
func (l Link) Base() string {
	return "https://" + net.JoinHostPort(l.Host, strconv.Itoa(int(l.Port)))
}

// ParseLink parses https://<host>:<port>/enroll#ca=<64 hex characters>.
// A link with user information, a query or a missing port is refused: the
// code never travels in the link (transport.md 6.1).
func ParseLink(s string) (Link, error) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Path != "/enroll" || u.Port() == "" {
		return Link{}, ErrLink
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil || port == 0 {
		return Link{}, ErrLink
	}
	frag, err := url.ParseQuery(u.Fragment)
	fp := frag.Get("ca")
	if err != nil || !fingerprintPattern.MatchString(fp) {
		return Link{}, ErrLink
	}
	return Link{Host: u.Hostname(), Port: uint16(port), CAFingerprint: fp}, nil
}

// FetchCA downloads the CA certificate without verifying the connection,
// which is the only unverified request garret makes, and accepts it only if
// its SHA-256 equals the link's fingerprint.
func FetchCA(ctx context.Context, l Link) (*x509.Certificate, error) {
	client := &http.Client{Timeout: requestTimeout, CheckRedirect: noRedirects, Transport: &http.Transport{
		// The CA is not known yet; the fingerprint check below is what
		// authenticates it.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}, // #nosec G402 -- verified by fingerprint
	}}
	body, err := get(ctx, client, l.Base()+"/ca.crt", maxCABytes)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(body)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%w: no certificate in the response", ErrFingerprint)
	}
	sum := sha256.Sum256(block.Bytes)
	want, _ := hex.DecodeString(l.CAFingerprint)
	if subtle.ConstantTimeCompare(sum[:], want) != 1 {
		return nil, ErrFingerprint
	}
	return x509.ParseCertificate(block.Bytes)
}

// ClientFor returns an HTTP client that trusts only ca, for host, and
// presents cert when it is not nil.
func ClientFor(ca *x509.Certificate, host string, cert *tls.Certificate) *http.Client {
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	cfg := &tls.Config{RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS13}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
	}
	return &http.Client{Timeout: requestTimeout, CheckRedirect: noRedirects, Transport: &http.Transport{TLSClientConfig: cfg}}
}

// noRedirects stops the client at the first response: a redirect would
// resend the code and password to a URL the link did not name.
func noRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// NewKey generates the device's ECDSA P-256 key. It never leaves the device.
func NewKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// CSR builds a certificate request for key. The server takes only the key
// from it.
func CSR(key *ecdsa.PrivateKey) ([]byte, error) {
	return x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
}

// Request holds what the member typed at the prompts.
type Request struct {
	Code        string
	Username    string
	DisplayName string
	Password    string
	Device      string
}

// Result is the server's answer to an enrollment or a renewal.
type Result struct {
	Certificate *x509.Certificate
	CA          *x509.Certificate
	ServerURL   string // the main service, for Git and the browser
}

// Enroll sends the request with a CSR for key to the enrollment listener,
// trusting only ca.
func Enroll(ctx context.Context, l Link, ca *x509.Certificate, req Request, key *ecdsa.PrivateKey) (Result, error) {
	csr, err := CSR(key)
	if err != nil {
		return Result{}, err
	}
	payload := map[string]any{"code": req.Code, "username": req.Username, "display_name": req.DisplayName,
		"password": req.Password, "device": req.Device, "csr": csr}
	var out struct {
		Certificate string `json:"certificate"`
		ServerURL   string `json:"server_url"`
	}
	if err := post(ctx, ClientFor(ca, l.Host, nil), l.Base()+"/enroll", payload, &out); err != nil {
		return Result{}, err
	}
	if err := checkServerURL(out.ServerURL, l.Host); err != nil {
		return Result{}, err
	}
	// The response's CA is ignored: only the CA pinned by the link is kept.
	return parseResult(out.Certificate, ca, out.ServerURL, key)
}

// Renew asks the main service for a new certificate for newKey, over a
// connection that presents the current certificate.
func Renew(ctx context.Context, serverURL string, ca *x509.Certificate, current tls.Certificate, newKey *ecdsa.PrivateKey) (Result, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return Result{}, err
	}
	if err := checkServerURL(serverURL, u.Hostname()); err != nil {
		return Result{}, err
	}
	csr, err := CSR(newKey)
	if err != nil {
		return Result{}, err
	}
	var out struct {
		Certificate string `json:"certificate"`
	}
	if err := post(ctx, ClientFor(ca, u.Hostname(), &current), serverURL+"/api/v1/me/certificates/renew",
		map[string]any{"csr": csr}, &out); err != nil {
		return Result{}, err
	}
	return parseResult(out.Certificate, ca, serverURL, newKey)
}

// checkServerURL accepts only https://<host>:<port> for the link's host, so
// a server cannot widen the Git configuration's scope or downgrade renewal.
func checkServerURL(s, host string) error {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Hostname() != host || u.Port() == "" || u.User != nil ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return fmt.Errorf("enroll: the server returned an unusable URL %q", s)
	}
	if port, err := strconv.ParseUint(u.Port(), 10, 16); err != nil || port == 0 {
		return fmt.Errorf("enroll: the server returned an unusable URL %q", s)
	}
	return nil
}

func parseResult(certPEM string, ca *x509.Certificate, serverURL string, key *ecdsa.PrivateKey) (Result, error) {
	cert, err := parsePEMCert(certPEM)
	if err != nil {
		return Result{}, err
	}
	if pub, ok := cert.PublicKey.(*ecdsa.PublicKey); !ok || !pub.Equal(&key.PublicKey) {
		return Result{}, errors.New("enroll: the issued certificate is not for this device's key")
	}
	if err := cert.CheckSignatureFrom(ca); err != nil {
		return Result{}, fmt.Errorf("enroll: the issued certificate is not from the CA: %w", err)
	}
	return Result{Certificate: cert, CA: ca, ServerURL: serverURL}, nil
}

func parsePEMCert(s string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(s))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("enroll: malformed certificate in the response")
	}
	return x509.ParseCertificate(block.Bytes)
}

func get(ctx context.Context, client *http.Client, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("enroll: connect to %s: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s", ErrRejected, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func post(ctx context.Context, client *http.Client, u string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("enroll: connect: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		return fmt.Errorf("%w: %s", ErrRejected, e.Error.Message)
	}
	return json.Unmarshal(data, out)
}
