package enroll

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/garret/internal/pki"
)

func TestParseLink(t *testing.T) {
	fp := strings.Repeat("ab", 32)
	l, err := ParseLink("https://review.local:8444/enroll#ca=" + fp)
	if err != nil || l.Host != "review.local" || l.Port != 8444 || l.CAFingerprint != fp {
		t.Fatalf("ParseLink = %+v, %v", l, err)
	}
	for _, bad := range []string{
		"http://review.local:8444/enroll#ca=" + fp,
		"https://review.local/enroll#ca=" + fp,
		"https://review.local:8444/enroll#ca=xyz",
		"https://review.local:8444/enroll",
		"https://review.local:8444/enroll?token=x#ca=" + fp,
		"https://user@review.local:8444/enroll#ca=" + fp,
		"not a link",
	} {
		if _, err := ParseLink(bad); !errors.Is(err, ErrLink) {
			t.Errorf("ParseLink(%q) = %v", bad, err)
		}
	}
}

// testServer runs a TLS server with a garret-like CA and the two
// enrollment routes, and returns its link.
func testServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, ca *pki.CA), mtls ...bool) (Link, *pki.CA) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := pki.NewSerial(rand.Reader)
	names := pki.Names{Host: "localhost", Networks: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}}
	tmpl, _ := pki.CATemplate(serial, time.Now(), names)
	caCert, _ := pki.Sign(tmpl, tmpl, &key.PublicKey, key, rand.Reader)
	ca := &pki.CA{Cert: caCert, Key: key}
	skey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	s2, _ := pki.NewSerial(rand.Reader)
	stmpl, _ := pki.ServerTemplate(s2, time.Now(), names)
	scert, _ := pki.Sign(stmpl, caCert, &skey.PublicKey, key, rand.Reader)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler(w, r, ca) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{scert.Raw}, PrivateKey: skey, Leaf: scert}}, MinVersion: tls.VersionTLS13}
	if len(mtls) > 0 && mtls[0] {
		pool := x509.NewCertPool()
		pool.AddCert(caCert)
		srv.TLS.ClientCAs = pool
		srv.TLS.ClientAuth = tls.RequireAndVerifyClientCert
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	sum := sha256.Sum256(caCert.Raw)
	return Link{Host: "localhost", Port: uint16(port), CAFingerprint: hex.EncodeToString(sum[:])}, ca
}

func enrollHandler(w http.ResponseWriter, r *http.Request, ca *pki.CA) {
	switch r.URL.Path {
	case "/ca.crt":
		_, _ = w.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Cert.Raw}))
	case "/enroll":
		var in struct {
			Code string `json:"code"`
			CSR  []byte `json:"csr"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		req, err := pki.CheckCSR(in.CSR)
		if err != nil || in.Code != "marble-orbit-candle-velvet-thimble-anchor" {
			http.Error(w, `{"error":{"code":"invalid_invitation","message":"the invitation code is not valid"}}`, http.StatusBadRequest)
			return
		}
		s, _ := pki.NewSerial(rand.Reader)
		tmpl, _ := pki.ClientTemplate(s, time.Now(), "01JD000000000000000000BOB0")
		cert, _ := pki.Sign(tmpl, ca.Cert, req.PublicKey, ca.Key, rand.Reader)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"certificate": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})),
			"ca":          string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Cert.Raw})),
			"server_url":  "https://localhost:8443",
		})
	default:
		http.NotFound(w, r)
	}
}

func TestFetchCAVerifiesFingerprint(t *testing.T) {
	link, ca := testServer(t, enrollHandler)
	got, err := FetchCA(context.Background(), link)
	if err != nil || !got.Equal(ca.Cert) {
		t.Fatalf("FetchCA = %v, %v", got, err)
	}
	link.CAFingerprint = strings.Repeat("00", 32)
	if _, err := FetchCA(context.Background(), link); !errors.Is(err, ErrFingerprint) {
		t.Fatalf("wrong fingerprint: %v", err)
	}
}

func TestEnrollAndSave(t *testing.T) {
	link, ca := testServer(t, enrollHandler)
	ctx := context.Background()
	caCert, err := FetchCA(ctx, link)
	if err != nil {
		t.Fatal(err)
	}
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Enroll(ctx, link, caCert, Request{Code: "marble-orbit-candle-velvet-thimble-anchor", Username: "bob",
		DisplayName: "Bob", Password: "correct horse battery staple", Device: "laptop"}, key)
	if err != nil {
		t.Fatal(err)
	}
	if res.ServerURL != "https://localhost:8443" || !res.Certificate.PublicKey.(*ecdsa.PublicKey).Equal(&key.PublicKey) {
		t.Fatalf("result %+v", res)
	}
	if _, err := Enroll(ctx, link, caCert, Request{Code: "wrong"}, key); !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), "invitation code") {
		t.Fatalf("wrong code: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "client", "localhost-8443")
	files := FilesIn(dir)
	if err := files.Save(key, res.Certificate, ca.Cert); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{files.Key, files.Cert, files.CA} {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", p, err, fi.Mode())
		}
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode %v", fi.Mode())
	}
	if data, _ := os.ReadFile(files.Key); !strings.HasPrefix(string(data), "-----BEGIN EC PRIVATE KEY-----") {
		t.Fatalf("key file is not SEC1: %.40s", data)
	}
	k2, c2, err := files.Load()
	if err != nil || !k2.Equal(key) || !c2.Equal(res.Certificate) {
		t.Fatalf("Load = %v", err)
	}
}

func TestGitConfigArgs(t *testing.T) {
	files := FilesIn("/home/b/garret/client/review.local-8443")
	args := GitConfigArgs("https://review.local:8443", files)
	joined := ""
	for _, a := range args {
		joined += strings.Join(a, " ") + "\n"
	}
	for _, want := range []string{
		"config --global http.https://review.local:8443/.sslCert /home/b/garret/client/review.local-8443/cert.pem",
		"config --global http.https://review.local:8443/.sslKey /home/b/garret/client/review.local-8443/key.pem",
		"config --global http.https://review.local:8443/.sslCAInfo /home/b/garret/client/review.local-8443/ca.pem",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
}

// issue signs the request's key with ca, or with foreign's key when set.
func issue(w http.ResponseWriter, r *http.Request, ca *pki.CA, foreign *ecdsa.PrivateKey) {
	issueWith(w, r, ca, foreign, ca.Cert, "https://localhost:8443")
}

// issueWith lets a test change the CA and server URL the response carries.
func issueWith(w http.ResponseWriter, r *http.Request, ca *pki.CA, foreign *ecdsa.PrivateKey, respCA *x509.Certificate, serverURL string) {
	var in struct {
		CSR []byte `json:"csr"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	req, err := pki.CheckCSR(in.CSR)
	if err != nil {
		http.Error(w, `{"error":{"message":"bad csr"}}`, http.StatusBadRequest)
		return
	}
	pub := req.PublicKey
	if foreign != nil {
		pub = &foreign.PublicKey
	}
	s, _ := pki.NewSerial(rand.Reader)
	tmpl, _ := pki.ClientTemplate(s, time.Now(), "01JD000000000000000000BOB0")
	cert, _ := pki.Sign(tmpl, ca.Cert, pub, ca.Key, rand.Reader)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"certificate": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})),
		"ca":          string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: respCA.Raw})),
		"server_url":  serverURL,
	})
}

func TestEnrollRejectsCertificateForAnotherKey(t *testing.T) {
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	link, ca := testServer(t, func(w http.ResponseWriter, r *http.Request, ca *pki.CA) { issue(w, r, ca, other) })
	key, _ := NewKey()
	if _, err := Enroll(context.Background(), link, ca.Cert, Request{}, key); err == nil || !strings.Contains(err.Error(), "not for this device") {
		t.Fatalf("foreign key certificate accepted: %v", err)
	}
}

func TestEnrollTrustsOnlyTheFetchedCA(t *testing.T) {
	link, _ := testServer(t, enrollHandler)
	_, other := testServer(t, enrollHandler)
	key, _ := NewKey()
	_, err := Enroll(context.Background(), link, other.Cert, Request{Code: "marble-orbit-candle-velvet-thimble-anchor"}, key)
	if err == nil || errors.Is(err, ErrRejected) {
		t.Fatalf("server under another CA accepted: %v", err)
	}
}

func TestRenewPresentsTheCurrentCertificate(t *testing.T) {
	var path, origin, ctype string
	var peers int
	link, ca := testServer(t, func(w http.ResponseWriter, r *http.Request, ca *pki.CA) {
		path, peers = r.URL.Path, len(r.TLS.PeerCertificates)
		origin, ctype = r.Header.Get("Origin"), r.Header.Get("Content-Type")
		issue(w, r, ca, nil)
	}, true)
	ctx := context.Background()
	oldKey, _ := NewKey()
	s, _ := pki.NewSerial(rand.Reader)
	tmpl, _ := pki.ClientTemplate(s, time.Now(), "01JD000000000000000000BOB0")
	oldCert, _ := pki.Sign(tmpl, ca.Cert, &oldKey.PublicKey, ca.Key, rand.Reader)
	current := tls.Certificate{Certificate: [][]byte{oldCert.Raw}, PrivateKey: oldKey, Leaf: oldCert}
	newKey, _ := NewKey()
	res, err := Renew(ctx, link.Base(), ca.Cert, current, newKey)
	if err != nil {
		t.Fatal(err)
	}
	if origin != link.Base() || ctype != "application/json" {
		t.Fatalf("renew headers Origin %q Content-Type %q", origin, ctype)
	}
	if path != "/api/v1/me/certificates/renew" || peers != 1 || !res.Certificate.PublicKey.(*ecdsa.PublicKey).Equal(&newKey.PublicKey) {
		t.Fatalf("renew path %q peers %d", path, peers)
	}
	if _, err := Renew(ctx, link.Base(), ca.Cert, tls.Certificate{}, newKey); err == nil {
		t.Fatal("renewal without a client certificate succeeded")
	}
}

func TestClientDir(t *testing.T) {
	tests := []struct {
		goos, xdg, want string
	}{
		{"darwin", "", "/h/Library/Application Support/garret/client/review.local-8443"},
		{"darwin", "/x", "/h/Library/Application Support/garret/client/review.local-8443"},
		{"linux", "", "/h/.config/garret/client/review.local-8443"},
		{"linux", "/x", "/x/garret/client/review.local-8443"},
	}
	for _, tt := range tests {
		if got := ClientDir(tt.goos, "/h", tt.xdg, "review.local", 8443); got != tt.want {
			t.Errorf("ClientDir(%s, %q) = %s, want %s", tt.goos, tt.xdg, got, tt.want)
		}
	}
}

func TestEnrollKeepsThePinnedCA(t *testing.T) {
	_, other := testServer(t, enrollHandler)
	link, ca := testServer(t, func(w http.ResponseWriter, r *http.Request, ca *pki.CA) {
		issueWith(w, r, ca, nil, other.Cert, "https://localhost:8443")
	})
	key, _ := NewKey()
	res, err := Enroll(context.Background(), link, ca.Cert, Request{}, key)
	if err != nil || !res.CA.Equal(ca.Cert) {
		t.Fatalf("result CA is not the pinned one: %v", err)
	}
}

func TestEnrollValidatesServerURL(t *testing.T) {
	for _, bad := range []string{"", "http://localhost:8443", "https://localhost", "https://evil.example:8443",
		"https://*.com:8443", "https://localhost:8443/x", "https://localhost:8443?a=b", "https://u@localhost:8443"} {
		link, ca := testServer(t, func(w http.ResponseWriter, r *http.Request, ca *pki.CA) { issueWith(w, r, ca, nil, ca.Cert, bad) })
		key, _ := NewKey()
		if _, err := Enroll(context.Background(), link, ca.Cert, Request{}, key); err == nil {
			t.Errorf("server_url %q accepted", bad)
		}
	}
}

func TestEnrollDoesNotFollowRedirects(t *testing.T) {
	var hits int
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	t.Cleanup(target.Close)
	link, ca := testServer(t, func(w http.ResponseWriter, r *http.Request, _ *pki.CA) {
		http.Redirect(w, r, target.URL+"/enroll", http.StatusPermanentRedirect)
	})
	key, _ := NewKey()
	if _, err := Enroll(context.Background(), link, ca.Cert, Request{Code: "x", Password: "p"}, key); err == nil || hits != 0 {
		t.Fatalf("redirect followed: hits %d, err %v", hits, err)
	}
}

func TestLinkBaseBracketsIPv6(t *testing.T) {
	if got := (Link{Host: "fe80::1", Port: 8444}).Base(); got != "https://[fe80::1]:8444" {
		t.Fatalf("Base = %s", got)
	}
}
