package auth

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/pki"
	"github.com/0xmhha/garret/internal/store/sqlcgen"
)

// Role is a user's role.
type Role string

// Roles of the first release.
const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// Certificate issuance paths, stored in client_certificates.issued_via.
const (
	viaSetup      = "setup"
	viaInvitation = "invitation"
	viaRenewal    = "renewal"
)

// InvitationLifetime is how long an invitation can be used (transport.md,
// T-D3).
const InvitationLifetime = 30 * time.Minute

const maxNameChars = 64

// User is an account as services see it.
type User struct {
	ID          string
	Username    string
	DisplayName string
	Role        Role
	Disabled    bool
	CreatedAt   time.Time
}

// IsAdmin reports whether u is an enabled administrator.
func (u User) IsAdmin() bool { return u.Role == RoleAdmin && !u.Disabled }

// NewAccount is the input for creating an account.
type NewAccount struct {
	Username    string
	DisplayName string
	Password    string
	Device      string
}

// Issued is the result of creating an account with its first device.
type Issued struct {
	User        User
	Certificate *x509.Certificate
	CA          *x509.Certificate
}

// Invitation is an invitation without its token.
type Invitation struct {
	ID        string
	Role      Role
	CreatedBy string
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
	UsedAt    *time.Time
	UsedBy    string
}

// SetupToken returns the single-use token for creating the first
// administrator, generating it on first call. It returns "" once an
// administrator exists.
func (s *Service) SetupToken() (string, error) {
	var admins int64
	err := s.db.ReadTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		admins, err = sqlcgen.New(tx).CountActiveAdmins(context.Background())
		return err
	})
	if err != nil {
		return "", err
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if admins > 0 {
		s.setupToken = ""
		return "", nil
	}
	if s.setupToken == "" {
		if s.setupToken, err = NewSecret(s.rand); err != nil {
			return "", err
		}
	}
	return s.setupToken, nil
}

// SetupAdmin creates the first administrator with the setup token. It
// returns ErrSetupClosed for a wrong token or once an administrator exists.
func (s *Service) SetupAdmin(ctx context.Context, token string, acct NewAccount, csrDER []byte) (Issued, error) {
	s.setupMu.Lock()
	want := s.setupToken
	s.setupMu.Unlock()
	a, b := Digest(token), Digest(want)
	if want == "" || !DigestEqual(a[:], b[:]) {
		return Issued{}, ErrSetupClosed
	}
	issued, err := s.createAccount(ctx, acct, csrDER, RoleAdmin, viaSetup, accountHooks{
		before: func(ctx context.Context, q *sqlcgen.Queries) error {
			n, err := q.CountActiveAdmins(ctx)
			if err == nil && n > 0 {
				return ErrSetupClosed
			}
			return err
		},
	})
	if err != nil {
		return Issued{}, err
	}
	s.setupMu.Lock()
	s.setupToken = ""
	s.setupMu.Unlock()
	return issued, nil
}

// CreateInvitation creates an invitation for role and returns it with its
// code, which is shown once and stored only as the digest of its normalized
// form. The administrator gives the code to the member apart from the link.
func (s *Service) CreateInvitation(ctx context.Context, actor User, role Role) (Invitation, string, error) {
	if !actor.IsAdmin() {
		return Invitation{}, "", ErrForbidden
	}
	if role != RoleAdmin && role != RoleMember {
		return Invitation{}, "", fmt.Errorf("%w: role", ErrInvalid)
	}
	token, err := NewInvitationCode(s.rand)
	if err != nil {
		return Invitation{}, "", err
	}
	invID, err := s.ids.New()
	if err != nil {
		return Invitation{}, "", err
	}
	now := s.clock.Now()
	digest := Digest(NormalizeCode(token))
	inv := Invitation{ID: invID, Role: role, CreatedBy: actor.ID, CreatedAt: ms(now), ExpiresAt: ms(now.Add(InvitationLifetime))}
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := sqlcgen.New(tx).InsertInvitation(ctx, sqlcgen.InsertInvitationParams{
			ID: invID, TokenSha256: digest[:], Role: string(role), CreatedBy: actor.ID,
			CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(InvitationLifetime).UnixMilli(),
		}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ActorID: actor.ID, Action: "invitation.created",
			SubjectKind: "invitation", SubjectID: invID, Data: map[string]any{"role": role}})
	})
	if err != nil {
		return Invitation{}, "", err
	}
	return inv, token, nil
}

// RevokeInvitation revokes an unused invitation.
func (s *Service) RevokeInvitation(ctx context.Context, actor User, invitationID string) error {
	if !actor.IsAdmin() {
		return ErrForbidden
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		n, err := sqlcgen.New(tx).RevokeInvitation(ctx, sqlcgen.RevokeInvitationParams{
			RevokedAt: sql.NullInt64{Int64: s.clock.Now().UnixMilli(), Valid: true}, ID: invitationID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return s.audit.Record(ctx, tx, audit.Event{ActorID: actor.ID, Action: "invitation.revoked",
			SubjectKind: "invitation", SubjectID: invitationID})
	})
}

// ListInvitations lists every invitation, newest first.
func (s *Service) ListInvitations(ctx context.Context, actor User) ([]Invitation, error) {
	if !actor.IsAdmin() {
		return nil, ErrForbidden
	}
	var rows []sqlcgen.Invitation
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		rows, err = sqlcgen.New(tx).ListInvitations(ctx)
		return err
	})
	out := make([]Invitation, len(rows))
	for i, r := range rows {
		out[i] = Invitation{ID: r.ID, Role: Role(r.Role), CreatedBy: r.CreatedBy, CreatedAt: fromMS(r.CreatedAt),
			ExpiresAt: fromMS(r.ExpiresAt), RevokedAt: optMS(r.RevokedAt), UsedAt: optMS(r.UsedAt), UsedBy: r.UsedBy.String}
	}
	return out, err
}

// OpenInvitationsUntil reports whether any invitation can still be used and,
// if so, when the earliest one expires. The enrollment listener is open
// exactly while this reports true.
func (s *Service) OpenInvitationsUntil(ctx context.Context) (time.Time, bool, error) {
	var earliest int64
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		earliest, err = sqlcgen.New(tx).EarliestOpenInvitationExpiry(ctx, s.clock.Now().UnixMilli())
		return err
	})
	if err != nil || earliest == 0 {
		return time.Time{}, false, err
	}
	return fromMS(earliest), true, nil
}

// Enroll creates an account from an invitation code and issues its first
// device certificate. An unknown, expired, revoked or used code all return
// ErrInvalidInvitation, and every failure counts against the remote address
// like a failed login. Nothing is written when any step fails.
func (s *Service) Enroll(ctx context.Context, remote netip.Addr, code string, acct NewAccount, csrDER []byte) (Issued, error) {
	addrKey := "enroll:" + remote.String()
	if !s.limiter.attempt(addrKey) {
		return Issued{}, ErrInvalidInvitation
	}
	issued, err := s.enroll(ctx, code, acct, csrDER)
	if err == nil {
		s.limiter.succeeded("", addrKey)
	}
	return issued, err
}

func (s *Service) enroll(ctx context.Context, code string, acct NewAccount, csrDER []byte) (Issued, error) {
	digest := Digest(NormalizeCode(code))
	var role Role
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		inv, err := sqlcgen.New(tx).GetInvitationByDigest(ctx, digest[:])
		if errors.Is(err, sql.ErrNoRows) || err == nil && !usable(inv, s.clock.Now()) {
			return ErrInvalidInvitation
		}
		role = Role(inv.Role)
		return err
	})
	if err != nil {
		return Issued{}, err
	}
	return s.createAccount(ctx, acct, csrDER, role, viaInvitation, accountHooks{after: func(ctx context.Context, q *sqlcgen.Queries, userID string) error {
		inv, err := q.GetInvitationByDigest(ctx, digest[:])
		if err != nil {
			return ErrInvalidInvitation
		}
		now := s.clock.Now().UnixMilli()
		n, err := q.UseInvitation(ctx, sqlcgen.UseInvitationParams{
			UsedAt: sql.NullInt64{Int64: now, Valid: true}, UsedBy: sql.NullString{String: userID, Valid: true},
			ID: inv.ID, ExpiresAt: now})
		if err == nil && n != 1 {
			return ErrInvalidInvitation
		}
		return err
	}})
}

func usable(inv sqlcgen.Invitation, now time.Time) bool {
	return !inv.UsedAt.Valid && !inv.RevokedAt.Valid && inv.ExpiresAt > now.UnixMilli()
}

// accountHooks run inside createAccount's transaction: before the user is
// inserted, and after the user and certificate rows exist.
type accountHooks struct {
	before func(context.Context, *sqlcgen.Queries) error
	after  func(context.Context, *sqlcgen.Queries, string) error
}

// createAccount validates, hashes and signs outside the transaction, then
// inserts the user and certificate with the hooks inside it.
func (s *Service) createAccount(ctx context.Context, acct NewAccount, csrDER []byte, role Role, via string,
	hooks accountHooks) (Issued, error) {
	if err := validateAccount(acct); err != nil {
		return Issued{}, err
	}
	hash, err := HashPassword(acct.Password, s.password, s.rand)
	if err != nil {
		return Issued{}, err
	}
	userID, err := s.ids.New()
	if err != nil {
		return Issued{}, err
	}
	cert, err := s.sign(csrDER, userID)
	if err != nil {
		return Issued{}, err
	}
	now := s.clock.Now()
	user := User{ID: userID, Username: acct.Username, DisplayName: acct.DisplayName, Role: role, CreatedAt: ms(now)}
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		if hooks.before != nil {
			if err := hooks.before(ctx, q); err != nil {
				return err
			}
		}
		if _, err := q.GetUserByUsername(ctx, acct.Username); err == nil {
			return fmt.Errorf("%w: username is taken", ErrConflict)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := q.InsertUser(ctx, sqlcgen.InsertUserParams{ID: userID, Username: acct.Username,
			DisplayName: acct.DisplayName, PasswordHash: hash, Role: string(role), CreatedAt: now.UnixMilli()}); err != nil {
			return mapUnique(err)
		}
		if err := s.insertCertificate(ctx, q, cert, userID, acct.Device, via, ""); err != nil {
			return err
		}
		if hooks.after != nil {
			if err := hooks.after(ctx, q, userID); err != nil {
				return err
			}
		}
		if err := s.audit.Record(ctx, tx, audit.Event{ActorID: userID, Action: "user.created", SubjectKind: "user",
			SubjectID: userID, Data: map[string]any{"role": role, "via": via}}); err != nil {
			return err
		}
		return s.recordIssued(ctx, tx, userID, cert, via)
	})
	if err != nil {
		return Issued{}, err
	}
	if err := s.RefreshActive(ctx); err != nil {
		return Issued{}, err
	}
	return Issued{User: user, Certificate: cert, CA: s.ca.Cert}, nil
}

func validateAccount(a NewAccount) error {
	if err := ValidUsername(a.Username); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := ValidPassword(a.Password); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if !validName(a.DisplayName) {
		return fmt.Errorf("%w: display name must be 1-64 characters", ErrInvalid)
	}
	if !validName(a.Device) {
		return fmt.Errorf("%w: device name must be 1-64 characters", ErrInvalid)
	}
	return nil
}

func validName(s string) bool {
	n := utf8.RuneCountInString(s)
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && n <= maxNameChars
}

// sign checks the request and issues a client certificate for userID. Only
// the request's public key is used.
func (s *Service) sign(csrDER []byte, userID string) (*x509.Certificate, error) {
	req, err := pki.CheckCSR(csrDER)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	serial, err := pki.NewSerial(s.rand)
	if err != nil {
		return nil, err
	}
	tmpl, err := pki.ClientTemplate(serial, s.clock.Now(), userID)
	if err != nil {
		return nil, err
	}
	return pki.Sign(tmpl, s.ca.Cert, req.PublicKey, s.ca.Key, s.rand)
}

func (s *Service) insertCertificate(ctx context.Context, q *sqlcgen.Queries, cert *x509.Certificate, userID, device, via, renewedFrom string) error {
	keyDigest := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return q.InsertCertificate(ctx, sqlcgen.InsertCertificateParams{
		Serial: pki.SerialString(cert.SerialNumber), UserID: userID, DeviceName: device,
		PublicKeySha256: keyDigest[:], IssuedVia: via,
		RenewedFrom: sql.NullString{String: renewedFrom, Valid: renewedFrom != ""},
		NotBefore:   cert.NotBefore.UnixMilli(), NotAfter: cert.NotAfter.UnixMilli(),
	})
}

func (s *Service) recordIssued(ctx context.Context, tx *sql.Tx, userID string, cert *x509.Certificate, via string) error {
	return s.audit.Record(ctx, tx, audit.Event{ActorID: userID, Action: "certificate.issued", SubjectKind: "certificate",
		SubjectID: pki.SerialString(cert.SerialNumber), Data: map[string]any{"via": via, "user": userID}})
}

// RenewCertificate issues a new certificate for actor's device identified by
// currentSerial, which must be the actor's, unrevoked and unexpired. The old
// certificate stays valid until it expires.
func (s *Service) RenewCertificate(ctx context.Context, actor User, currentSerial string, csrDER []byte) (*x509.Certificate, error) {
	var device string
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		c, err := sqlcgen.New(tx).GetCertificate(ctx, currentSerial)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		if c.UserID != actor.ID || actor.Disabled || c.RevokedAt.Valid || s.clock.Now().UnixMilli() >= c.NotAfter {
			return ErrForbidden
		}
		device = c.DeviceName
		return nil
	})
	if err != nil {
		return nil, err
	}
	cert, err := s.sign(csrDER, actor.ID)
	if err != nil {
		return nil, err
	}
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		// Check again under the write lock, so a revocation or a disable that
		// committed after the read above cannot be bypassed by renewal.
		c, err := q.GetCertificate(ctx, currentSerial)
		if err != nil {
			return err
		}
		u, err := q.GetUserByID(ctx, actor.ID)
		if err != nil {
			return err
		}
		if c.RevokedAt.Valid || u.DisabledAt.Valid {
			return ErrForbidden
		}
		if err := s.insertCertificate(ctx, q, cert, actor.ID, device, viaRenewal, currentSerial); err != nil {
			return err
		}
		return s.recordIssued(ctx, tx, actor.ID, cert, viaRenewal)
	})
	if err != nil {
		return nil, err
	}
	return cert, s.RefreshActive(ctx)
}

// Certificate is a stored device certificate.
type Certificate struct {
	Serial     string
	UserID     string
	DeviceName string
	IssuedVia  string
	NotBefore  time.Time
	NotAfter   time.Time
	RevokedAt  *time.Time
}

// ListCertificates lists the certificates of userID; members see only their
// own.
func (s *Service) ListCertificates(ctx context.Context, actor User, userID string) ([]Certificate, error) {
	if actor.ID != userID && !actor.IsAdmin() {
		return nil, ErrForbidden
	}
	var rows []sqlcgen.ClientCertificate
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		rows, err = sqlcgen.New(tx).ListUserCertificates(ctx, userID)
		return err
	})
	out := make([]Certificate, len(rows))
	for i, r := range rows {
		out[i] = Certificate{Serial: r.Serial, UserID: r.UserID, DeviceName: r.DeviceName, IssuedVia: r.IssuedVia,
			NotBefore: fromMS(r.NotBefore), NotAfter: fromMS(r.NotAfter), RevokedAt: optMS(r.RevokedAt)}
	}
	return out, err
}

// RevokeCertificate revokes a certificate of the actor, or any certificate
// when the actor is an administrator.
func (s *Service) RevokeCertificate(ctx context.Context, actor User, serial string) error {
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		c, err := q.GetCertificate(ctx, serial)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if c.UserID != actor.ID && !actor.IsAdmin() {
			return ErrForbidden
		}
		if _, err := q.RevokeCertificate(ctx, sqlcgen.RevokeCertificateParams{
			RevokedAt: sql.NullInt64{Int64: s.clock.Now().UnixMilli(), Valid: true}, Serial: serial}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ActorID: actor.ID, Action: "certificate.revoked",
			SubjectKind: "certificate", SubjectID: serial, Data: map[string]any{"user": c.UserID}})
	})
	if err != nil {
		return err
	}
	return s.RefreshActive(ctx)
}

// CertificateUser returns the enabled user of an active, unexpired
// certificate, or ErrForbidden.
func (s *Service) CertificateUser(ctx context.Context, serial string) (User, error) {
	var user User
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		c, err := q.GetCertificate(ctx, serial)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		if c.RevokedAt.Valid || s.clock.Now().UnixMilli() >= c.NotAfter {
			return ErrForbidden
		}
		u, err := q.GetUserByID(ctx, c.UserID)
		if err != nil {
			return err
		}
		user = toUser(u)
		if user.Disabled {
			return ErrForbidden
		}
		return nil
	})
	return user, err
}

// RefreshActive loads the active serials from the database into the set
// that TLS handshakes consult. Services call it after every commit that
// issues or revokes a certificate; the server calls it once at startup.
func (s *Service) RefreshActive(ctx context.Context) error {
	var serials []string
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		serials, err = sqlcgen.New(tx).ListActiveSerials(ctx)
		return err
	})
	if err != nil {
		return err
	}
	s.active.Replace(serials)
	return nil
}

func toUser(u sqlcgen.User) User {
	return User{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: Role(u.Role),
		Disabled: u.DisabledAt.Valid, CreatedAt: fromMS(u.CreatedAt)}
}

func mapUnique(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: users.username") {
		return fmt.Errorf("%w: username is taken", ErrConflict)
	}
	return err
}

func ms(t time.Time) time.Time { return time.UnixMilli(t.UnixMilli()).UTC() }
func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }
func optMS(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromMS(v.Int64)
	return &t
}

// ListUsers returns every account by username. Any member may list users,
// so the interface can show names; only administrators see who is disabled.
func (s *Service) ListUsers(ctx context.Context, actor User) ([]User, error) {
	var rows []sqlcgen.User
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		rows, err = sqlcgen.New(tx).ListUsers(ctx)
		return err
	})
	out := make([]User, len(rows))
	for i, r := range rows {
		out[i] = toUser(r)
		if !actor.IsAdmin() {
			out[i].Disabled = false
		}
	}
	return out, err
}
