package httpapi

import (
	"net/http"
	"testing"
)

func TestAdminCreatesAccountAndMemberChangesPassword(t *testing.T) {
	e := newEnv(t, false)
	root := &client{e: e}
	root.login(t)
	w := root.do(t, http.MethodPost, basePath+"/admin/users", map[string]string{"username": "dave", "display_name": "Dave", "role": "member"}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	created := decodeBody[struct {
		User     userJSON `json:"user"`
		Password string   `json:"password"`
	}](t, w.Body.Bytes())
	if created.User.Username != "dave" || created.Password == "" {
		t.Fatalf("created %+v", created)
	}

	dave := &client{e: e}
	w = dave.do(t, http.MethodPost, basePath+"/auth/login", map[string]string{"username": "dave", "password": created.Password}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("login with the temporary password %d %s", w.Code, w.Body)
	}
	dave.adopt(t, w)
	const next = "a much longer passphrase now"
	if w := dave.do(t, http.MethodPost, basePath+"/me/password", map[string]string{"current": "wrong wrong wrong wrong", "new": next}, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("wrong current password %d, want 400 (not 401: the session is fine)", w.Code)
	}
	w = dave.do(t, http.MethodPost, basePath+"/me/password", map[string]string{"current": created.Password, "new": next}, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("change %d %s", w.Code, w.Body)
	}
	if w := dave.do(t, http.MethodGet, basePath+"/me", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("session after a password change %d, want 401", w.Code)
	}
	if w := dave.do(t, http.MethodPost, basePath+"/auth/login", map[string]string{"username": "dave", "password": next}, nil); w.Code != http.StatusOK {
		t.Fatalf("login with the new password %d", w.Code)
	}
}

func TestAdminCreateAccountRefusals(t *testing.T) {
	body := map[string]string{"username": "dave", "display_name": "Dave", "role": "member"}
	on := newEnv(t, true)
	root := &client{e: on, cert: on.admin.Certificate}
	root.login(t)
	if w := root.do(t, http.MethodPost, basePath+"/admin/users", body, nil); w.Code != http.StatusConflict {
		t.Fatalf("with client certificates on %d, want 409", w.Code)
	}
}
