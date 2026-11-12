package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/pki"
	"github.com/0xmhha/garret/internal/store"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

var start = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

type env struct {
	svc    *Service
	db     *store.DB
	clock  *testClock
	ca     *pki.CA
	active *pki.ActiveSerials
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "garret.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	c := &testClock{now: start}
	ids, err := id.NewGenerator(c, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := pki.NewSerial(rand.Reader)
	tmpl, err := pki.CATemplate(serial, start, pki.Names{Host: "h.local", Networks: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}})
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := pki.Sign(tmpl, tmpl, &key.PublicKey, key, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &pki.CA{Cert: caCert, Key: key}
	active := &pki.ActiveSerials{}
	svc, err := NewService(Config{
		DB: db, Audit: audit.NewRecorder(ids, c), IDs: ids, Clock: c, Rand: rand.Reader,
		CA: ca, Active: active, Password: fast,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &env{svc: svc, db: db, clock: c, ca: ca, active: active}
}

func newCSR(t *testing.T) []byte {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, k)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

const goodPassword = "correct horse battery staple"

func account(name string) NewAccount {
	return NewAccount{Username: name, DisplayName: strings.ToUpper(name), Password: goodPassword, Device: name + "-laptop"}
}

// admin sets up the first administrator.
func (e *env) admin(t *testing.T) User {
	t.Helper()
	token, err := e.svc.SetupToken()
	if err != nil || token == "" {
		t.Fatalf("SetupToken = %q, %v", token, err)
	}
	issued, err := e.svc.SetupAdmin(context.Background(), token, account("root"), newCSR(t))
	if err != nil {
		t.Fatal(err)
	}
	return issued.User
}

func (e *env) member(t *testing.T, admin User, name string) Issued {
	t.Helper()
	_, token, err := e.svc.CreateInvitation(context.Background(), admin, RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := e.svc.Enroll(context.Background(), addr, token, account(name), newCSR(t))
	if err != nil {
		t.Fatal(err)
	}
	return issued
}

func TestSetupAdmin(t *testing.T) {
	e := newEnv(t)
	token, err := e.svc.SetupToken()
	if err != nil || token == "" {
		t.Fatal("no setup token before the first administrator")
	}
	if _, err := e.svc.SetupAdmin(context.Background(), "wrong", account("root"), newCSR(t)); !errors.Is(err, ErrSetupClosed) {
		t.Fatalf("wrong token: %v", err)
	}
	issued, err := e.svc.SetupAdmin(context.Background(), token, account("root"), newCSR(t))
	if err != nil {
		t.Fatal(err)
	}
	if issued.User.Role != RoleAdmin || issued.Certificate == nil || issued.CA != e.ca.Cert {
		t.Fatalf("issued %+v", issued)
	}
	if !e.active.Active(pki.SerialString(issued.Certificate.SerialNumber)) {
		t.Fatal("setup certificate not active")
	}
	if _, err := e.svc.SetupAdmin(context.Background(), token, account("root2"), newCSR(t)); !errors.Is(err, ErrSetupClosed) {
		t.Fatalf("token reused: %v", err)
	}
	if again, _ := e.svc.SetupToken(); again != "" {
		t.Fatal("setup token offered after an administrator exists")
	}
}

func TestEnroll(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	issued := e.member(t, admin, "bob")
	if issued.User.Role != RoleMember || issued.User.Username != "bob" {
		t.Fatalf("user %+v", issued.User)
	}
	cert := issued.Certificate
	if cert.Subject.CommonName != issued.User.ID || !e.active.Active(pki.SerialString(cert.SerialNumber)) {
		t.Fatalf("certificate %v", cert.Subject)
	}
	roots := x509.NewCertPool()
	roots.AddCert(e.ca.Cert)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: start.Add(time.Hour),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatal(err)
	}
	u, err := e.svc.CertificateUser(context.Background(), pki.SerialString(cert.SerialNumber))
	if err != nil || u.ID != issued.User.ID {
		t.Fatalf("CertificateUser = %+v, %v", u, err)
	}
}

func TestEnrollRejectsBadInvitations(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	ctx := context.Background()
	_, used, _ := e.svc.CreateInvitation(ctx, admin, RoleMember)
	if _, err := e.svc.Enroll(ctx, addr, used, account("bob"), newCSR(t)); err != nil {
		t.Fatal(err)
	}
	inv, revoked, _ := e.svc.CreateInvitation(ctx, admin, RoleMember)
	if err := e.svc.RevokeInvitation(ctx, admin, inv.ID); err != nil {
		t.Fatal(err)
	}
	_, expired, _ := e.svc.CreateInvitation(ctx, admin, RoleMember)
	e.clock.advance(InvitationLifetime + time.Second)
	for name, token := range map[string]string{"used": used, "revoked": revoked, "expired": expired, "unknown": "nope"} {
		if _, err := e.svc.Enroll(ctx, addr, token, account("carol"), newCSR(t)); !errors.Is(err, ErrInvalidInvitation) {
			t.Errorf("%s: err = %v, want ErrInvalidInvitation", name, err)
		}
	}
}

func TestEnrollValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	ctx := context.Background()
	tests := map[string]NewAccount{
		"bad username":  {Username: "Bob", DisplayName: "B", Password: goodPassword, Device: "d"},
		"short pass":    {Username: "bob", DisplayName: "B", Password: "short", Device: "d"},
		"empty display": {Username: "bob", DisplayName: "", Password: goodPassword, Device: "d"},
		"long display":  {Username: "bob", DisplayName: strings.Repeat("x", 65), Password: goodPassword, Device: "d"},
		"empty device":  {Username: "bob", DisplayName: "B", Password: goodPassword, Device: ""},
	}
	for name, acct := range tests {
		_, token, _ := e.svc.CreateInvitation(ctx, admin, RoleMember)
		if _, err := e.svc.Enroll(ctx, addr, token, acct, newCSR(t)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	_, token, _ := e.svc.CreateInvitation(ctx, admin, RoleMember)
	if _, err := e.svc.Enroll(ctx, addr, token, account("bob"), []byte("garbage")); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad CSR: %v", err)
	}
	if _, err := e.svc.Enroll(ctx, addr, token, account("root"), newCSR(t)); !errors.Is(err, ErrConflict) {
		t.Errorf("taken username: %v", err)
	}
	// The failed attempts must not have used the invitation.
	if _, err := e.svc.Enroll(ctx, addr, token, account("bob"), newCSR(t)); err != nil {
		t.Errorf("invitation consumed by a failed attempt: %v", err)
	}
}

func TestEnrollConcurrentUseOfOneInvitation(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	_, token, _ := e.svc.CreateInvitation(context.Background(), admin, RoleMember)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"bob", "carol"} {
		wg.Go(func() {
			_, err := e.svc.Enroll(context.Background(), addr, token, account(name), newCSR(t))
			results <- err
		})
	}
	wg.Wait()
	close(results)
	ok := 0
	for err := range results {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrInvalidInvitation) {
			t.Fatalf("unexpected error %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d enrollments succeeded with one invitation", ok)
	}
}

func TestInvitationsAdminOnly(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	bob := e.member(t, admin, "bob").User
	ctx := context.Background()
	if _, _, err := e.svc.CreateInvitation(ctx, bob, RoleMember); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member created an invitation: %v", err)
	}
	if _, err := e.svc.ListInvitations(ctx, bob); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member listed invitations: %v", err)
	}
	if _, _, err := e.svc.CreateInvitation(ctx, admin, Role("owner")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown role: %v", err)
	}
}

func TestOpenInvitationsUntil(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	ctx := context.Background()
	if _, open, err := e.svc.OpenInvitationsUntil(ctx); err != nil || open {
		t.Fatalf("open = %v, %v with no invitations", open, err)
	}
	if _, _, err := e.svc.CreateInvitation(ctx, admin, RoleMember); err != nil {
		t.Fatal(err)
	}
	until, open, err := e.svc.OpenInvitationsUntil(ctx)
	if err != nil || !open || !until.Equal(start.Add(InvitationLifetime)) {
		t.Fatalf("until %v open %v err %v", until, open, err)
	}
	e.clock.advance(InvitationLifetime)
	if _, open, _ := e.svc.OpenInvitationsUntil(ctx); open {
		t.Fatal("expired invitation still open")
	}
}

func TestRenewCertificate(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	bob := e.member(t, admin, "bob")
	carol := e.member(t, admin, "carol")
	ctx := context.Background()
	old := pki.SerialString(bob.Certificate.SerialNumber)
	e.clock.advance(4 * 24 * time.Hour)
	renewed, err := e.svc.RenewCertificate(ctx, bob.User, old, newCSR(t))
	if err != nil {
		t.Fatal(err)
	}
	if renewed.NotBefore.Before(e.clock.Now().Add(-10*time.Minute)) || !e.active.Active(pki.SerialString(renewed.SerialNumber)) {
		t.Fatalf("renewed %v", renewed.NotBefore)
	}
	if !e.active.Active(old) {
		t.Fatal("the old certificate stopped working at renewal")
	}
	if _, err := e.svc.RenewCertificate(ctx, carol.User, old, newCSR(t)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("renewing another user's certificate: %v", err)
	}
	e.clock.advance(4 * 24 * time.Hour)
	if _, err := e.svc.RenewCertificate(ctx, bob.User, old, newCSR(t)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("renewing an expired certificate: %v", err)
	}
}

func TestRevokeCertificate(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	bob := e.member(t, admin, "bob")
	carol := e.member(t, admin, "carol")
	ctx := context.Background()
	serial := pki.SerialString(bob.Certificate.SerialNumber)
	if err := e.svc.RevokeCertificate(ctx, carol.User, serial); !errors.Is(err, ErrForbidden) {
		t.Fatalf("another member revoked it: %v", err)
	}
	if err := e.svc.RevokeCertificate(ctx, bob.User, serial); err != nil {
		t.Fatal(err)
	}
	if e.active.Active(serial) {
		t.Fatal("revoked certificate still active")
	}
	if _, err := e.svc.CertificateUser(ctx, serial); !errors.Is(err, ErrForbidden) {
		t.Fatalf("CertificateUser on a revoked certificate: %v", err)
	}
	if _, err := e.svc.RenewCertificate(ctx, bob.User, serial, newCSR(t)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("renewed a revoked certificate: %v", err)
	}
	if err := e.svc.RevokeCertificate(ctx, admin, pki.SerialString(carol.Certificate.SerialNumber)); err != nil {
		t.Fatalf("admin revoke: %v", err)
	}
}

func TestRefreshActiveAtStartup(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	bob := e.member(t, admin, "bob")
	fresh := &pki.ActiveSerials{}
	e.svc.active = fresh
	if err := e.svc.RefreshActive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !fresh.Active(pki.SerialString(bob.Certificate.SerialNumber)) {
		t.Fatal("RefreshActive did not load stored serials")
	}
}

func TestEnrollCodeIsWordsAndTypingIsForgiving(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	_, code, err := e.svc.CreateInvitation(context.Background(), admin, RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.Split(code, "-")) != codeWords {
		t.Fatalf("code %q", code)
	}
	typed := strings.ToUpper(strings.ReplaceAll(code, "-", "  "))
	if _, err := e.svc.Enroll(context.Background(), addr, typed, account("bob"), newCSR(t)); err != nil {
		t.Fatalf("code typed with spaces and capitals: %v", err)
	}
}

func TestEnrollRateLimitedPerAddress(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	ctx := context.Background()
	for range maxFailures {
		_, _ = e.svc.Enroll(ctx, addr, "wrong-code", account("bob"), newCSR(t))
	}
	_, code, _ := e.svc.CreateInvitation(ctx, admin, RoleMember)
	if _, err := e.svc.Enroll(ctx, addr, code, account("bob"), newCSR(t)); !errors.Is(err, ErrInvalidInvitation) {
		t.Fatalf("enrollment allowed while the address is limited: %v", err)
	}
	other := netip.MustParseAddr("192.168.1.77")
	if _, err := e.svc.Enroll(ctx, other, code, account("bob"), newCSR(t)); err != nil {
		t.Fatalf("another address blocked: %v", err)
	}
}

func TestListUsers(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	bob := e.member(t, admin, "bob").User
	if err := e.svc.DisableUser(context.Background(), admin, bob.ID); err != nil {
		t.Fatal(err)
	}
	asAdmin, err := e.svc.ListUsers(context.Background(), admin)
	if err != nil || len(asAdmin) != 2 || !asAdmin[0].Disabled || asAdmin[0].Username != "bob" {
		t.Fatalf("admin view %+v, %v", asAdmin, err)
	}
	carol := e.member(t, admin, "carol").User
	asMember, _ := e.svc.ListUsers(context.Background(), carol)
	for _, u := range asMember {
		if u.Disabled {
			t.Fatalf("member sees disabled flag: %+v", u)
		}
	}
}
