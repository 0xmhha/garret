package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/store/sqlcgen"
)

// viaAdmin marks an account an administrator created directly.
const viaAdmin = "admin"

// NewUser is an account an administrator creates without an invitation.
type NewUser struct {
	Username    string
	DisplayName string
	Role        Role
}

// CreateUser creates an account with no device certificate and returns it
// with a temporary password, shown only now: six words from the invitation
// wordlist, so it is as strong as an invitation code. It is allowed only
// while client certificates are off, since otherwise the account could not
// connect (security.md, Turning client certificates off). Only
// administrators may call it.
func (s *Service) CreateUser(ctx context.Context, actor User, in NewUser) (User, string, error) {
	if !actor.IsAdmin() {
		return User{}, "", ErrForbidden
	}
	if s.clientCerts {
		return User{}, "", fmt.Errorf("%w: client certificates are on; members join with garret enroll", ErrConflict)
	}
	if err := ValidUsername(in.Username); err != nil {
		return User{}, "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if !validName(in.DisplayName) {
		return User{}, "", fmt.Errorf("%w: display name must be 1-64 characters", ErrInvalid)
	}
	if in.Role != RoleAdmin && in.Role != RoleMember {
		return User{}, "", fmt.Errorf("%w: role", ErrInvalid)
	}
	password, err := NewInvitationCode(s.rand)
	if err != nil {
		return User{}, "", err
	}
	hash, err := HashPassword(password, s.password, s.rand)
	if err != nil {
		return User{}, "", err
	}
	userID, err := s.ids.New()
	if err != nil {
		return User{}, "", err
	}
	now := s.clock.Now()
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		if _, err := q.GetUserByUsername(ctx, in.Username); err == nil {
			return fmt.Errorf("%w: username is taken", ErrConflict)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := q.InsertUser(ctx, sqlcgen.InsertUserParams{ID: userID, Username: in.Username,
			DisplayName: in.DisplayName, PasswordHash: hash, Role: string(in.Role), CreatedAt: now.UnixMilli()}); err != nil {
			return mapUnique(err)
		}
		return s.audit.Record(ctx, tx, audit.Event{ActorID: actor.ID, Action: "user.created", SubjectKind: "user",
			SubjectID: userID, Data: map[string]any{"role": in.Role, "via": viaAdmin}})
	})
	if err != nil {
		return User{}, "", err
	}
	return User{ID: userID, Username: in.Username, DisplayName: in.DisplayName, Role: in.Role, CreatedAt: ms(now)}, password, nil
}

// ChangePassword replaces actor's password after checking the current one,
// ends every session of actor, this one included, and revokes actor's
// access tokens: whoever knew the old password could have made one. A wrong current
// password counts like a failed login and returns ErrInvalid, not
// ErrInvalidCredentials: the session is valid, and the interface must not
// read the answer as being signed out.
func (s *Service) ChangePassword(ctx context.Context, actor User, current, next string) error {
	if err := ValidPassword(next); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if current == next {
		return fmt.Errorf("%w: the new password is the current one", ErrInvalid)
	}
	key := "password:" + actor.ID
	wrong := fmt.Errorf("%w: the current password is wrong", ErrInvalid)
	if !s.limiter.attempt(key) {
		return wrong
	}
	var u sqlcgen.User
	if err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		u, err = sqlcgen.New(tx).GetUserByID(ctx, actor.ID)
		return err
	}); err != nil {
		return err
	}
	ok, _, err := VerifyPassword(current, u.PasswordHash, s.password)
	if err != nil || !ok {
		return wrong
	}
	s.limiter.succeeded("", key)
	hash, err := HashPassword(next, s.password, s.rand)
	if err != nil {
		return err
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		if err := q.UpdatePasswordHash(ctx, sqlcgen.UpdatePasswordHashParams{PasswordHash: hash, ID: actor.ID}); err != nil {
			return err
		}
		if err := q.DeleteUserSessions(ctx, actor.ID); err != nil {
			return err
		}
		if err := q.RevokeUserAccessTokens(ctx, sqlcgen.RevokeUserAccessTokensParams{
			RevokedAt: sql.NullInt64{Int64: s.clock.Now().UnixMilli(), Valid: true}, UserID: actor.ID}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ActorID: actor.ID, Action: "user.password_changed", SubjectKind: "user", SubjectID: actor.ID})
	})
}
