package httpapi

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/pki"
	"github.com/0xmhha/garret/internal/store"
)

type wall struct{}

func (wall) Now() time.Time { return time.Now() }

const origin = "https://review.local:8443"

const password = "correct horse battery staple"

type env struct {
	api     *API
	storeDB *store.DB
	auth    *auth.Service
	active  *pki.ActiveSerials
	admin   auth.Issued
	ca      *pki.CA
}

func newEnv(t *testing.T, clientCerts bool) *env {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "garret.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ids, _ := id.NewGenerator(wall{}, rand.Reader)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := pki.NewSerial(rand.Reader)
	tmpl, _ := pki.CATemplate(serial, time.Now(), pki.Names{Host: "review.local", Networks: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}})
	caCert, _ := pki.Sign(tmpl, tmpl, &key.PublicKey, key, rand.Reader)
	active := &pki.ActiveSerials{}
	svc, err := auth.NewService(auth.Config{DB: db, Audit: audit.NewRecorder(ids, wall{}), IDs: ids, Clock: wall{}, Rand: rand.Reader,
		CA: &pki.CA{Cert: caCert, Key: key}, Active: active,
		Password: auth.Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}, ClientCertsOff: !clientCerts})
	if err != nil {
		t.Fatal(err)
	}
	token, _ := svc.SetupToken()
	admin, err := svc.SetupAdmin(ctx, token, auth.NewAccount{Username: "root", DisplayName: "Root", Password: password, Device: "host"}, csr(t))
	if err != nil {
		t.Fatal(err)
	}
	api := New(Config{Auth: svc, Origin: origin, ClientCerts: clientCerts, Active: active, Log: slog.New(slog.DiscardHandler)})
	return &env{api: api, storeDB: db, auth: svc, active: active, admin: admin, ca: &pki.CA{Cert: caCert, Key: key}}
}

func csr(t *testing.T) []byte {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, k)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// client keeps a session cookie and CSRF token, and presents a verified
// client certificate when cert is set.
type client struct {
	e      *env
	cookie *http.Cookie
	csrf   string
	cert   *x509.Certificate
}

func (c *client) do(t *testing.T, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, r)
	req.RemoteAddr = "192.168.1.9:50000"
	if c.cert != nil {
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{c.cert}}}
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", origin)
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	if c.csrf != "" && method != http.MethodGet {
		req.Header.Set(csrfHeader, c.csrf)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	c.e.api.ServeHTTP(w, req)
	return w
}

func (c *client) login(t *testing.T) {
	t.Helper()
	w := c.do(t, http.MethodPost, basePath+"/auth/login", map[string]string{"username": "root", "password": password}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("login %d %s", w.Code, w.Body)
	}
	for _, ck := range w.Result().Cookies() {
		if ck.Name == sessionCookie {
			if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" || ck.Domain != "" {
				t.Fatalf("cookie attributes %+v", ck)
			}
			c.cookie = ck
		}
	}
	var out struct {
		CSRF string `json:"csrf_token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	c.csrf = out.CSRF
	if c.cookie == nil || c.csrf == "" {
		t.Fatal("no session")
	}
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var b errorBody
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	return b.Error.Code
}

func TestLoginMeLogout(t *testing.T) {
	e := newEnv(t, false)
	c := &client{e: e}
	c.login(t)
	w := c.do(t, http.MethodGet, basePath+"/me", nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"username":"root"`) {
		t.Fatalf("me %d %s", w.Code, w.Body)
	}
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "X-Frame-Options"} {
		if w.Header().Get(h) == "" {
			t.Errorf("missing %s", h)
		}
	}
	if w := c.do(t, http.MethodPost, basePath+"/auth/logout", nil, nil); w.Code != http.StatusNoContent {
		t.Fatalf("logout %d %s", w.Code, w.Body)
	}
	if w := c.do(t, http.MethodGet, basePath+"/me", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("after logout %d", w.Code)
	}
}

func TestLoginFailureIsGeneric(t *testing.T) {
	e := newEnv(t, false)
	c := &client{e: e}
	for _, creds := range []map[string]string{{"username": "root", "password": "wrong password here"}, {"username": "nobody", "password": password}} {
		w := c.do(t, http.MethodPost, basePath+"/auth/login", creds, nil)
		if w.Code != http.StatusUnauthorized || errorCode(t, w) != codeUnauthenticated || strings.Contains(w.Body.String(), "nobody") {
			t.Fatalf("login failure %d %s", w.Code, w.Body)
		}
	}
}

func TestStateChangesNeedOriginAndCSRF(t *testing.T) {
	e := newEnv(t, false)
	c := &client{e: e}
	c.login(t)
	if w := c.do(t, http.MethodPost, basePath+"/me/tokens", map[string]string{"name": "git"}, map[string]string{"Origin": "https://evil.example"}); w.Code != http.StatusForbidden {
		t.Fatalf("foreign origin %d", w.Code)
	}
	noCSRF := &client{e: e, cookie: c.cookie}
	if w := noCSRF.do(t, http.MethodPost, basePath+"/me/tokens", map[string]string{"name": "git"}, nil); w.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF %d", w.Code)
	}
	if w := c.do(t, http.MethodPost, basePath+"/me/tokens", map[string]string{"name": "git"}, map[string]string{csrfHeader: "wrong"}); w.Code != http.StatusForbidden {
		t.Fatalf("wrong CSRF %d", w.Code)
	}
	if w := c.do(t, http.MethodPost, basePath+"/auth/login", map[string]string{"username": "root", "password": password}, map[string]string{"Origin": ""}); w.Code != http.StatusForbidden {
		t.Fatalf("login without origin %d", w.Code)
	}
}

func TestTokens(t *testing.T) {
	e := newEnv(t, false)
	c := &client{e: e}
	c.login(t)
	w := c.do(t, http.MethodPost, basePath+"/me/tokens", map[string]string{"name": "git"}, nil)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"secret":"`) {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var out struct {
		Token tokenJSON `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	list := c.do(t, http.MethodGet, basePath+"/me/tokens", nil, nil)
	if strings.Contains(list.Body.String(), `"secret"`) || !strings.Contains(list.Body.String(), out.Token.ID) {
		t.Fatalf("list %s", list.Body)
	}
	if w := c.do(t, http.MethodDelete, basePath+"/me/tokens/"+out.Token.ID, nil, nil); w.Code != http.StatusNoContent {
		t.Fatalf("revoke %d %s", w.Code, w.Body)
	}
	if w := c.do(t, http.MethodPost, basePath+"/me/tokens", map[string]any{"name": "x", "extra": 1}, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown field %d", w.Code)
	}
	big := map[string]string{"name": strings.Repeat("x", maxJSONBody)}
	if w := c.do(t, http.MethodPost, basePath+"/me/tokens", big, nil); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body %d", w.Code)
	}
}

func TestClientCertificatesRequiredAndRechecked(t *testing.T) {
	e := newEnv(t, true)
	noCert := &client{e: e}
	if w := noCert.do(t, http.MethodPost, basePath+"/auth/login", map[string]string{"username": "root", "password": password}, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("login without certificate %d", w.Code)
	}
	c := &client{e: e, cert: e.admin.Certificate}
	c.login(t)
	if w := c.do(t, http.MethodGet, basePath+"/me", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("me with certificate %d %s", w.Code, w.Body)
	}
	e.active.Replace(nil) // revoked after the connection was opened
	if w := c.do(t, http.MethodGet, basePath+"/me", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("request on a revoked certificate %d", w.Code)
	}
}

func TestRenewUsesHandshakeSerial(t *testing.T) {
	e := newEnv(t, true)
	c := &client{e: e, cert: e.admin.Certificate}
	c.login(t)
	w := c.do(t, http.MethodPost, basePath+"/me/certificates/renew", map[string]string{"csr": base64.StdEncoding.EncodeToString(csr(t))},
		map[string]string{"Content-Type": "application/json"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "BEGIN CERTIFICATE") {
		t.Fatalf("renew %d %s", w.Code, w.Body)
	}
	list := c.do(t, http.MethodGet, basePath+"/me/certificates", nil, nil)
	if strings.Count(list.Body.String(), `"serial"`) != 2 || !strings.Contains(list.Body.String(), `"current":true`) {
		t.Fatalf("certificates %s", list.Body)
	}
	certsOff := newEnv(t, false)
	o := &client{e: certsOff}
	o.login(t)
	if w := o.do(t, http.MethodPost, basePath+"/me/certificates/renew", map[string]string{"csr": base64.StdEncoding.EncodeToString(csr(t))}, nil); w.Code != http.StatusForbidden {
		t.Fatalf("renew without a certificate on the connection %d", w.Code)
	}
}

func TestRenewWithTheCertificateAlone(t *testing.T) {
	e := newEnv(t, true)
	c := &client{e: e, cert: e.admin.Certificate} // no login
	body := map[string]string{"csr": base64.StdEncoding.EncodeToString(csr(t))}
	jsonType := map[string]string{"Content-Type": "application/json"}
	if w := c.do(t, http.MethodPost, basePath+"/me/certificates/renew", body, jsonType); w.Code != http.StatusOK {
		t.Fatalf("renew without a session %d %s", w.Code, w.Body)
	}
	if w := c.do(t, http.MethodPost, basePath+"/me/certificates/renew", body, map[string]string{"Content-Type": "text/plain"}); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("renew as a simple request %d", w.Code)
	}
	if w := c.do(t, http.MethodPost, basePath+"/me/certificates/renew", body,
		map[string]string{"Content-Type": "application/json", "Origin": "https://evil.example"}); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin renew %d", w.Code)
	}
	noCert := &client{e: newEnv(t, false)}
	if w := noCert.do(t, http.MethodPost, basePath+"/me/certificates/renew", body, jsonType); w.Code != http.StatusForbidden {
		t.Fatalf("renew without a certificate %d", w.Code)
	}
}
