package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/store/sqlcgen"
)

var (
	// ErrInvalidCredentials is returned for every failed login or token
	// check, whatever the cause, so the response reveals nothing.
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	// ErrUnauthenticated is returned when a session is missing, expired or
	// does not match the client certificate.
	ErrUnauthenticated = errors.New("auth: not signed in")
)

// Session lifetimes (security.md).
const (
	SessionIdle = 7 * 24 * time.Hour
	SessionMax  = 30 * 24 * time.Hour
	// touchInterval throttles last_seen_at and last_used_at writes.
	touchInterval = time.Minute
)

// LoginInput is a login attempt. CertSerial is the verified client
// certificate's serial, empty when client certificates are off.
type LoginInput struct {
	Username   string
	Password   string
	CertSerial string
	Remote     netip.Addr
}

// Session is a new session. Secret goes into the cookie and CSRF into the
// header of state-changing requests; both are shown only now.
type Session struct {
	Secret string
	CSRF   string
	User   User
}

// Login checks the credentials and opens a session. Every failure returns
// ErrInvalidCredentials and counts against the username and the address.
func (s *Service) Login(ctx context.Context, in LoginInput) (Session, error) {
	userKey, addrKey := "user:"+in.Username, "addr:"+in.Remote.String()
	if !s.limiter.attempt(userKey, addrKey) {
		return Session{}, ErrInvalidCredentials
	}
	user, err := s.verifyLogin(ctx, in)
	if err != nil {
		return Session{}, err
	}
	s.limiter.succeeded(userKey, addrKey)
	return s.openSession(ctx, user, in.CertSerial)
}

func (s *Service) verifyLogin(ctx context.Context, in LoginInput) (sqlcgen.User, error) {
	var u sqlcgen.User
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		u, err = sqlcgen.New(tx).GetUserByUsername(ctx, in.Username)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		_, _, _ = VerifyPassword(in.Password, s.dummyHash, s.password)
		return u, ErrInvalidCredentials
	}
	if err != nil {
		return u, err
	}
	ok, rehash, err := VerifyPassword(in.Password, u.PasswordHash, s.password)
	if err != nil || !ok || u.DisabledAt.Valid {
		return u, ErrInvalidCredentials
	}
	if !s.certMatches(ctx, in.CertSerial, u.ID) {
		return u, ErrInvalidCredentials
	}
	if rehash {
		if err := s.rehash(ctx, u.ID, in.Password); err != nil {
			return u, err
		}
	}
	return u, nil
}

// certMatches reports whether serial is an active certificate of userID,
// or true when client certificates are off.
func (s *Service) certMatches(ctx context.Context, serial, userID string) bool {
	if !s.clientCerts {
		return true
	}
	if serial == "" {
		return false
	}
	owner, err := s.CertificateUser(ctx, serial)
	return err == nil && owner.ID == userID
}

func (s *Service) rehash(ctx context.Context, userID, password string) error {
	hash, err := HashPassword(password, s.password, s.rand)
	if err != nil {
		return err
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		return sqlcgen.New(tx).UpdatePasswordHash(ctx, sqlcgen.UpdatePasswordHashParams{PasswordHash: hash, ID: userID})
	})
}

func (s *Service) openSession(ctx context.Context, u sqlcgen.User, certSerial string) (Session, error) {
	secret, err := NewSecret(s.rand)
	if err != nil {
		return Session{}, err
	}
	csrf, err := NewSecret(s.rand)
	if err != nil {
		return Session{}, err
	}
	now := s.clock.Now()
	sd, cd := Digest(secret), Digest(csrf)
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := sqlcgen.New(tx).InsertSession(ctx, sqlcgen.InsertSessionParams{
			IDSha256: sd[:], UserID: u.ID, CertificateSerial: sql.NullString{String: certSerial, Valid: certSerial != ""},
			CsrfTokenSha256: cd[:], CreatedAt: now.UnixMilli(), LastSeenAt: now.UnixMilli(),
			ExpiresAt: now.Add(SessionMax).UnixMilli(),
		}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ActorID: u.ID, Action: "session.created", SubjectKind: "user", SubjectID: u.ID})
	})
	if err != nil {
		return Session{}, err
	}
	return Session{Secret: secret, CSRF: csrf, User: toUser(u)}, nil
}

// Authenticated is the result of checking a session.
type Authenticated struct {
	User       User
	csrfDigest []byte
}

// CheckCSRF reports whether token is the session's CSRF token.
func (a Authenticated) CheckCSRF(token string) bool {
	d := Digest(token)
	return DigestEqual(d[:], a.csrfDigest)
}

// Authenticate checks a session secret from the cookie. The session must be
// within its idle and absolute lifetimes, its user enabled, and certSerial an
// active certificate of the same user (when client certificates are on).
func (s *Service) Authenticate(ctx context.Context, secret, certSerial string) (Authenticated, error) {
	d := Digest(secret)
	now := s.clock.Now()
	var row sqlcgen.Session
	var u sqlcgen.User
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		var err error
		if row, err = q.GetSession(ctx, d[:]); err != nil {
			return err
		}
		u, err = q.GetUserByID(ctx, row.UserID)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Authenticated{}, ErrUnauthenticated
	}
	if err != nil {
		return Authenticated{}, err
	}
	if now.UnixMilli() >= row.ExpiresAt || now.Sub(fromMS(row.LastSeenAt)) >= SessionIdle ||
		u.DisabledAt.Valid || !s.certMatches(ctx, certSerial, u.ID) {
		return Authenticated{}, ErrUnauthenticated
	}
	if now.Sub(fromMS(row.LastSeenAt)) >= touchInterval {
		err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
			return sqlcgen.New(tx).TouchSession(ctx, sqlcgen.TouchSessionParams{
				LastSeenAt: now.UnixMilli(), IDSha256: d[:], LastSeenAt_2: now.Add(-touchInterval).UnixMilli()})
		})
		if err != nil {
			return Authenticated{}, err
		}
	}
	return Authenticated{User: toUser(u), csrfDigest: row.CsrfTokenSha256}, nil
}

// Logout ends the session with secret. Ending an unknown session is not an
// error.
func (s *Service) Logout(ctx context.Context, secret string) error {
	d := Digest(secret)
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		return sqlcgen.New(tx).DeleteSession(ctx, d[:])
	})
}

// SweepSessions deletes sessions past their absolute or idle lifetime and
// returns how many it removed.
func (s *Service) SweepSessions(ctx context.Context) (int64, error) {
	now := s.clock.Now()
	var n int64
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		n, err = sqlcgen.New(tx).DeleteExpiredSessions(ctx, sqlcgen.DeleteExpiredSessionsParams{
			ExpiresAt: now.UnixMilli(), LastSeenAt: now.Add(-SessionIdle).UnixMilli()})
		return err
	})
	return n, err
}

// Token is a personal access token without its secret.
type Token struct {
	ID         string
	Name       string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// CreateToken creates a personal access token named name for actor and
// returns its secret, which is shown only now.
func (s *Service) CreateToken(ctx context.Context, actor User, name string) (Token, string, error) {
	if !validName(name) {
		return Token{}, "", fmt.Errorf("%w: token name must be 1-64 characters", ErrInvalid)
	}
	secret, err := NewSecret(s.rand)
	if err != nil {
		return Token{}, "", err
	}
	tokenID, err := s.ids.New()
	if err != nil {
		return Token{}, "", err
	}
	now := s.clock.Now()
	d := Digest(secret)
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		err := sqlcgen.New(tx).InsertAccessToken(ctx, sqlcgen.InsertAccessTokenParams{
			ID: tokenID, UserID: actor.ID, Name: name, TokenSha256: d[:], CreatedAt: now.UnixMilli()})
		if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: access_tokens.user_id, access_tokens.name") {
			return fmt.Errorf("%w: a token with this name exists", ErrConflict)
		}
		if err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ActorID: actor.ID, Action: "token.created", SubjectKind: "token", SubjectID: tokenID})
	})
	if err != nil {
		return Token{}, "", err
	}
	return Token{ID: tokenID, Name: name, CreatedAt: ms(now)}, secret, nil
}

// ListTokens lists actor's tokens, newest first.
func (s *Service) ListTokens(ctx context.Context, actor User) ([]Token, error) {
	var rows []sqlcgen.AccessToken
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		rows, err = sqlcgen.New(tx).ListUserAccessTokens(ctx, actor.ID)
		return err
	})
	out := make([]Token, len(rows))
	for i, r := range rows {
		out[i] = Token{ID: r.ID, Name: r.Name, CreatedAt: fromMS(r.CreatedAt), LastUsedAt: optMS(r.LastUsedAt), RevokedAt: optMS(r.RevokedAt)}
	}
	return out, err
}

// RevokeToken revokes one of actor's tokens.
func (s *Service) RevokeToken(ctx context.Context, actor User, tokenID string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		t, err := q.GetAccessToken(ctx, tokenID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if t.UserID != actor.ID {
			return ErrForbidden
		}
		if _, err := q.RevokeAccessToken(ctx, sqlcgen.RevokeAccessTokenParams{
			RevokedAt: sql.NullInt64{Int64: s.clock.Now().UnixMilli(), Valid: true}, ID: tokenID}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ActorID: actor.ID, Action: "token.revoked", SubjectKind: "token", SubjectID: tokenID})
	})
}

// TokenInput is a Git request's credentials.
type TokenInput struct {
	Username   string
	Token      string
	CertSerial string
	Remote     netip.Addr
}

// AuthenticateToken checks a username and personal access token from HTTP
// Basic authentication. Every failure returns ErrInvalidCredentials and is
// rate-limited like a login.
func (s *Service) AuthenticateToken(ctx context.Context, in TokenInput) (User, error) {
	userKey, addrKey := "user:"+in.Username, "addr:"+in.Remote.String()
	if !s.limiter.attempt(userKey, addrKey) {
		return User{}, ErrInvalidCredentials
	}
	u, tokenID, err := s.verifyToken(ctx, in)
	if err != nil {
		return User{}, err
	}
	s.limiter.succeeded(userKey, addrKey)
	now := s.clock.Now()
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		return sqlcgen.New(tx).TouchAccessToken(ctx, sqlcgen.TouchAccessTokenParams{
			LastUsedAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, ID: tokenID,
			LastUsedAt_2: sql.NullInt64{Int64: now.Add(-touchInterval).UnixMilli(), Valid: true}})
	})
	return toUser(u), err
}

func (s *Service) verifyToken(ctx context.Context, in TokenInput) (sqlcgen.User, string, error) {
	d := Digest(in.Token)
	var t sqlcgen.AccessToken
	var u sqlcgen.User
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		var err error
		if t, err = q.GetAccessTokenByDigest(ctx, d[:]); err != nil {
			return err
		}
		u, err = q.GetUserByID(ctx, t.UserID)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return u, "", ErrInvalidCredentials
	}
	if err != nil {
		return u, "", err
	}
	// The digest lookup already matched the secret; these checks bind it to
	// the claimed username, an enabled account and the device certificate.
	if u.Username != in.Username || t.RevokedAt.Valid || u.DisabledAt.Valid || !s.certMatches(ctx, in.CertSerial, u.ID) {
		return u, "", ErrInvalidCredentials
	}
	return u, t.ID, nil
}

// DisableUser disables userID and, in the same transaction, deletes its
// sessions and revokes its tokens and certificates. The last enabled
// administrator cannot be disabled.
func (s *Service) DisableUser(ctx context.Context, actor User, userID string) error {
	if !actor.IsAdmin() {
		return ErrForbidden
	}
	now := sql.NullInt64{Int64: s.clock.Now().UnixMilli(), Valid: true}
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		target, err := q.GetUserByID(ctx, userID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if target.Role == string(RoleAdmin) {
			admins, err := q.CountActiveAdmins(ctx)
			if err != nil {
				return err
			}
			if admins <= 1 {
				return fmt.Errorf("%w: the last administrator cannot be disabled", ErrConflict)
			}
		}
		n, err := q.DisableUser(ctx, sqlcgen.DisableUserParams{DisabledAt: now, ID: userID})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: the user is already disabled", ErrConflict)
		}
		if err := q.DeleteUserSessions(ctx, userID); err != nil {
			return err
		}
		if err := q.RevokeUserAccessTokens(ctx, sqlcgen.RevokeUserAccessTokensParams{RevokedAt: now, UserID: userID}); err != nil {
			return err
		}
		if err := q.RevokeUserCertificates(ctx, sqlcgen.RevokeUserCertificatesParams{RevokedAt: now, UserID: userID}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ActorID: actor.ID, Action: "user.disabled", SubjectKind: "user", SubjectID: userID})
	})
	if err != nil {
		return err
	}
	if err := s.RefreshActive(ctx); err != nil {
		return err
	}
	s.onDisable(userID)
	return nil
}
