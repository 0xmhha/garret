package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xmhha/garret/internal/pki"
)

var addr = netip.MustParseAddr("192.168.1.9")

func serialOf(i Issued) string { return pki.SerialString(i.Certificate.SerialNumber) }

func (e *env) login(t *testing.T, name string, cert Issued) Session {
	t.Helper()
	s, err := e.svc.Login(context.Background(), LoginInput{Username: name, Password: goodPassword, CertSerial: serialOf(cert), Remote: addr})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLoginAndAuthenticate(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	bob := e.member(t, admin, "bob")
	s := e.login(t, "bob", bob)
	if s.Secret == "" || s.CSRF == "" || s.User.ID != bob.User.ID {
		t.Fatalf("session %+v", s)
	}
	got, err := e.svc.Authenticate(context.Background(), s.Secret, serialOf(bob))
	if err != nil || got.User.ID != bob.User.ID {
		t.Fatalf("Authenticate = %+v, %v", got, err)
	}
	if !got.CheckCSRF(s.CSRF) || got.CheckCSRF("wrong") {
		t.Fatal("CSRF check wrong")
	}
	if _, err := e.svc.Authenticate(context.Background(), "nope", serialOf(bob)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("unknown session: %v", err)
	}
	if err := e.svc.Logout(context.Background(), s.Secret); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Authenticate(context.Background(), s.Secret, serialOf(bob)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("after logout: %v", err)
	}
}

func TestLoginFailuresLookAlike(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	bob := e.member(t, admin, "bob")
	carol := e.member(t, admin, "carol")
	ctx := context.Background()
	cases := map[string]LoginInput{
		"wrong password":             {Username: "bob", Password: "wrong password here", CertSerial: serialOf(bob), Remote: addr},
		"unknown user":               {Username: "nobody", Password: goodPassword, CertSerial: serialOf(bob), Remote: addr},
		"another user's certificate": {Username: "bob", Password: goodPassword, CertSerial: serialOf(carol), Remote: addr},
		"no certificate":             {Username: "bob", Password: goodPassword, Remote: addr},
	}
	for name, in := range cases {
		if _, err := e.svc.Login(ctx, in); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("%s: err = %v, want ErrInvalidCredentials", name, err)
		}
	}
}

func TestLoginWithoutClientCertificates(t *testing.T) {
	e := newEnv(t)
	e.svc.clientCerts = false
	admin := e.admin(t)
	e.member(t, admin, "bob")
	if _, err := e.svc.Login(context.Background(), LoginInput{Username: "bob", Password: goodPassword, Remote: addr}); err != nil {
		t.Fatalf("login without certificates when they are off: %v", err)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	bob := e.member(t, admin, "bob")
	ctx := context.Background()
	bad := LoginInput{Username: "bob", Password: "wrong password here", CertSerial: serialOf(bob), Remote: addr}
	for range maxFailures {
		_, _ = e.svc.Login(ctx, bad)
	}
	good := bad
	good.Password = goodPassword
	if _, err := e.svc.Login(ctx, good); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("correct password accepted while limited: %v", err)
	}
	other := good
	other.Remote = netip.MustParseAddr("192.168.1.10")
	if _, err := e.svc.Login(ctx, other); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("username limit ignored from another address: %v", err)
	}
	e.clock.advance(failureWindow)
	if _, err := e.svc.Login(ctx, good); err != nil {
		t.Fatalf("still limited after the window: %v", err)
	}
}

func TestLimiterPerAddress(t *testing.T) {
	c := &testClock{now: start}
	l := newLimiter(c)
	for i := range maxFailures {
		l.fail("addr:a", "user:u"+string(rune('a'+i)))
	}
	if !l.blocked("addr:a", "user:new") {
		t.Fatal("address not limited after max failures across usernames")
	}
	if l.blocked("addr:b", "user:new") {
		t.Fatal("another address limited")
	}
	for i := range maxTrackedKeys + 10 {
		l.fail("addr:" + string(rune(i+1000)))
	}
	if n := l.size(); n > maxTrackedKeys {
		t.Fatalf("tracking %d keys, cap %d", n, maxTrackedKeys)
	}
}

func TestSessionLifetimes(t *testing.T) {
	e := newEnv(t)
	// Certificates expire after 7 days; this test is about sessions only.
	e.svc.clientCerts = false
	admin := e.admin(t)
	bob := e.member(t, admin, "bob")
	ctx := context.Background()
	s := e.login(t, "bob", bob)
	e.clock.advance(SessionIdle - time.Minute)
	if _, err := e.svc.Authenticate(ctx, s.Secret, serialOf(bob)); err != nil {
		t.Fatalf("active session rejected: %v", err)
	}
	e.clock.advance(SessionIdle - time.Minute)
	if _, err := e.svc.Authenticate(ctx, s.Secret, serialOf(bob)); err != nil {
		t.Fatalf("session refreshed by use was rejected: %v", err)
	}
	idle := e.login(t, "bob", bob)
	e.clock.advance(SessionIdle)
	if _, err := e.svc.Authenticate(ctx, idle.Secret, serialOf(bob)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("idle session accepted: %v", err)
	}
	if n, err := e.svc.SweepSessions(ctx); err != nil || n < 1 {
		t.Fatalf("sweep removed %d, %v", n, err)
	}
}

func TestSessionAbsoluteLimit(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	bob := e.member(t, admin, "bob")
	ctx := context.Background()
	s := e.login(t, "bob", bob)
	cert := serialOf(bob)
	for elapsed := time.Duration(0); elapsed < SessionMax; elapsed += 6 * 24 * time.Hour {
		e.clock.advance(6 * 24 * time.Hour)
		if e.clock.Now().Sub(start) >= SessionMax {
			break
		}
		renewed, err := e.svc.RenewCertificate(ctx, bob.User, cert, newCSR(t))
		if err != nil {
			t.Fatal(err)
		}
		cert = pki.SerialString(renewed.SerialNumber)
		if _, err := e.svc.Authenticate(ctx, s.Secret, cert); err != nil {
			t.Fatalf("at %v: %v", e.clock.Now().Sub(start), err)
		}
	}
	e.clock.advance(SessionMax)
	if _, err := e.svc.Authenticate(ctx, s.Secret, cert); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("session past its absolute limit: %v", err)
	}
}

func TestSessionCertificateBinding(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	bob := e.member(t, admin, "bob")
	carol := e.member(t, admin, "carol")
	s := e.login(t, "bob", bob)
	if _, err := e.svc.Authenticate(context.Background(), s.Secret, serialOf(carol)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("bob's session accepted with carol's certificate: %v", err)
	}
	if _, err := e.svc.Authenticate(context.Background(), s.Secret, ""); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("session accepted without a certificate: %v", err)
	}
}

func TestPasswordRehashOnLogin(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	bob := e.member(t, admin, "bob")
	stronger := fast
	stronger.Time = 2
	e.svc.password = stronger
	e.login(t, "bob", bob)
	var hash string
	_ = e.db.ReadTx(context.Background(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(context.Background(), "SELECT password_hash FROM users WHERE username = 'bob'").Scan(&hash)
	})
	if _, rehash, err := VerifyPassword(goodPassword, hash, stronger); err != nil || rehash {
		t.Fatalf("hash not upgraded: rehash=%v err=%v", rehash, err)
	}
}

func TestAccessTokens(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	bob := e.member(t, admin, "bob")
	carol := e.member(t, admin, "carol")
	ctx := context.Background()
	tok, secret, err := e.svc.CreateToken(ctx, bob.User, "laptop git")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.CreateToken(ctx, bob.User, "laptop git"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name: %v", err)
	}
	u, err := e.svc.AuthenticateToken(ctx, TokenInput{Username: "bob", Token: secret, CertSerial: serialOf(bob), Remote: addr})
	if err != nil || u.ID != bob.User.ID {
		t.Fatalf("AuthenticateToken = %+v, %v", u, err)
	}
	for name, in := range map[string]TokenInput{
		"other username":    {Username: "carol", Token: secret, CertSerial: serialOf(carol), Remote: addr},
		"other user's cert": {Username: "bob", Token: secret, CertSerial: serialOf(carol), Remote: addr},
		"wrong token":       {Username: "bob", Token: "nope", CertSerial: serialOf(bob), Remote: addr},
	} {
		if _, err := e.svc.AuthenticateToken(ctx, in); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := e.svc.RevokeToken(ctx, carol.User, tok.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("another user revoked the token: %v", err)
	}
	if err := e.svc.RevokeToken(ctx, bob.User, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AuthenticateToken(ctx, TokenInput{Username: "bob", Token: secret, CertSerial: serialOf(bob), Remote: addr}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("revoked token accepted: %v", err)
	}
	list, err := e.svc.ListTokens(ctx, bob.User)
	if err != nil || len(list) != 1 || list[0].RevokedAt == nil {
		t.Fatalf("ListTokens = %+v, %v", list, err)
	}
}

func TestDisableUser(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	bob := e.member(t, admin, "bob")
	ctx := context.Background()
	var closed []string
	e.svc.onDisable = func(id string) { closed = append(closed, id) }
	s := e.login(t, "bob", bob)
	_, secret, _ := e.svc.CreateToken(ctx, bob.User, "git")
	if err := e.svc.DisableUser(ctx, bob.User, admin.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member disabled the admin: %v", err)
	}
	if err := e.svc.DisableUser(ctx, admin, admin.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("last administrator disabled: %v", err)
	}
	if err := e.svc.DisableUser(ctx, admin, bob.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Authenticate(ctx, s.Secret, serialOf(bob)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("session survived: %v", err)
	}
	if _, err := e.svc.AuthenticateToken(ctx, TokenInput{Username: "bob", Token: secret, CertSerial: serialOf(bob), Remote: addr}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("token survived: %v", err)
	}
	if e.active.Active(serialOf(bob)) {
		t.Fatal("certificate still active")
	}
	if len(closed) != 1 || closed[0] != bob.User.ID {
		t.Fatalf("connections not closed: %v", closed)
	}
	if _, err := e.svc.Login(ctx, LoginInput{Username: "bob", Password: goodPassword, CertSerial: serialOf(bob), Remote: addr}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled user logged in: %v", err)
	}
}

func TestLimiterAttemptIsAtomic(t *testing.T) {
	l := newLimiter(&testClock{now: start})
	var wg sync.WaitGroup
	var allowed atomic.Int64
	for range 50 {
		wg.Go(func() {
			if l.attempt("user:bob", "addr:a") {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if n := allowed.Load(); n != maxFailures {
		t.Fatalf("%d of 50 concurrent attempts allowed, want %d", n, maxFailures)
	}
}

func TestSuccessfulLoginsDoNotExhaustTheAddress(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	bob := e.member(t, admin, "bob")
	for range maxFailures + 5 {
		e.login(t, "bob", bob)
	}
}

func TestConcurrentWrongPasswordsAreCapped(t *testing.T) {
	e := newEnv(t)
	admin := e.admin(t)
	bob := e.member(t, admin, "bob")
	var wg sync.WaitGroup
	for range 30 {
		wg.Go(func() {
			_, _ = e.svc.Login(context.Background(), LoginInput{Username: "bob", Password: "wrong password here", CertSerial: serialOf(bob), Remote: addr})
		})
	}
	wg.Wait()
	if !e.svc.limiter.blocked("user:bob") {
		t.Fatal("username not blocked after a burst of failures")
	}
	if _, err := e.svc.Login(context.Background(), LoginInput{Username: "bob", Password: goodPassword, CertSerial: serialOf(bob), Remote: addr}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("login allowed after the burst: %v", err)
	}
}
