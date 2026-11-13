package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestCreateUser(t *testing.T) {
	e := newEnv(t)
	e.svc.clientCerts = false
	admin := e.admin(t)
	ctx := context.Background()

	u, password, err := e.svc.CreateUser(ctx, admin, NewUser{Username: "dave", DisplayName: "Dave", Role: RoleMember})
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "dave" || u.DisplayName != "Dave" || u.Role != RoleMember || u.ID == "" {
		t.Fatalf("user %+v", u)
	}
	if n := len(strings.Split(password, "-")); n != codeWords {
		t.Fatalf("temporary password %q has %d words, want %d", password, n, codeWords)
	}
	if _, err := e.svc.Login(ctx, LoginInput{Username: "dave", Password: password, Remote: addr}); err != nil {
		t.Fatalf("login with the temporary password: %v", err)
	}
	users, err := e.svc.ListUsers(ctx, admin)
	if err != nil || len(users) != 2 {
		t.Fatalf("users %v, %v", users, err)
	}
}

func TestCreateUserRefusals(t *testing.T) {
	e := newEnv(t)
	e.svc.clientCerts = false
	admin := e.admin(t)
	member := e.member(t, admin, "bob").User
	ctx := context.Background()
	ok := NewUser{Username: "dave", DisplayName: "Dave", Role: RoleMember}
	tests := []struct {
		name  string
		actor User
		in    NewUser
		want  error
	}{
		{"member", member, ok, ErrForbidden},
		{"bad username", admin, NewUser{Username: "D", DisplayName: "Dave", Role: RoleMember}, ErrInvalid},
		{"empty display name", admin, NewUser{Username: "dave", DisplayName: " ", Role: RoleMember}, ErrInvalid},
		{"bad role", admin, NewUser{Username: "dave", DisplayName: "Dave", Role: "owner"}, ErrInvalid},
		{"taken username", admin, NewUser{Username: "bob", DisplayName: "Bob", Role: RoleMember}, ErrConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := e.svc.CreateUser(ctx, tt.actor, tt.in); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
	e.svc.clientCerts = true
	if _, _, err := e.svc.CreateUser(ctx, admin, ok); !errors.Is(err, ErrConflict) {
		t.Fatalf("with client certificates on: err = %v, want ErrConflict", err)
	}
}

func TestChangePassword(t *testing.T) {
	e := newEnv(t)
	e.svc.clientCerts = false
	admin := e.admin(t)
	bob := e.member(t, admin, "bob").User
	ctx := context.Background()
	if _, err := e.svc.Login(ctx, LoginInput{Username: "bob", Password: goodPassword, Remote: addr}); err != nil {
		t.Fatal(err)
	}
	const next = "a much longer passphrase now"
	tests := []struct {
		name          string
		current, next string
		want          error
	}{
		{"wrong current", "not the password at all", next, ErrInvalid},
		{"too short", goodPassword, "short", ErrInvalid},
		{"same as current", goodPassword, goodPassword, ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := e.svc.ChangePassword(ctx, bob, tt.current, tt.next); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
	if err := e.svc.ChangePassword(ctx, bob, goodPassword, next); err != nil {
		t.Fatal(err)
	}
	var sessions int
	if err := e.db.ReadTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT count(*) FROM sessions WHERE user_id = ?", bob.ID).Scan(&sessions)
	}); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 {
		t.Fatalf("%d sessions survive a password change", sessions)
	}
	if _, err := e.svc.Login(ctx, LoginInput{Username: "bob", Password: goodPassword, Remote: addr}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password still works: %v", err)
	}
	if _, err := e.svc.Login(ctx, LoginInput{Username: "bob", Password: next, Remote: addr}); err != nil {
		t.Fatalf("new password: %v", err)
	}
}
