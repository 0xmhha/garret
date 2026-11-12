package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnrollHandler(t *testing.T) {
	e := newEnv(t, false)
	ctx := context.Background()
	_, code, err := e.auth.CreateInvitation(ctx, e.admin.User, "member")
	if err != nil {
		t.Fatal(err)
	}
	enrolled := 0
	h := NewEnrollHandler(EnrollConfig{Auth: e.auth, CA: e.ca, Log: slog.New(slog.DiscardHandler), OnEnrolled: func() { enrolled++ }})
	do := func(method, path string, body any) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		r := httptest.NewRequestWithContext(ctx, method, path, &buf)
		r.RemoteAddr = "192.168.1.20:40000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := do(http.MethodGet, "/ca.crt", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "BEGIN CERTIFICATE") {
		t.Fatalf("ca.crt %d", w.Code)
	}
	req := map[string]string{"code": strings.ToUpper(strings.ReplaceAll(code, "-", " ")), "username": "bob", "display_name": "Bob",
		"password": password, "device": "laptop", "csr": base64.StdEncoding.EncodeToString(csr(t))}
	if w := do(http.MethodPost, "/enroll", req); w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"username":"bob"`) || enrolled != 1 {
		t.Fatalf("enroll %d %s", w.Code, w.Body)
	}
	if w := do(http.MethodPost, "/enroll", req); w.Code != http.StatusBadRequest || errorCode(t, w) != codeInvalidInvitation {
		t.Fatalf("reused code %d %s", w.Code, w.Body)
	}
	for _, path := range []string{basePath + "/me", basePath + "/auth/login", "/git/libfoo.git/info/refs"} {
		if w := do(http.MethodGet, path, nil); w.Code != http.StatusNotFound {
			t.Errorf("%s reachable on the enrollment listener: %d", path, w.Code)
		}
	}
}
