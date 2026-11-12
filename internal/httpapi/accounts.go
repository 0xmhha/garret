package httpapi

import (
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"time"

	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/pki"
)

type userJSON struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        auth.Role `json:"role"`
	Disabled    bool      `json:"disabled"`
	CreatedAt   time.Time `json:"created_at"`
}

func toUserJSON(u auth.User) userJSON {
	return userJSON{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: u.Role, Disabled: u.Disabled, CreatedAt: u.CreatedAt}
}

type tokenJSON struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

func toTokenJSON(t auth.Token) tokenJSON {
	return tokenJSON{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt}
}

type certificateJSON struct {
	Serial     string     `json:"serial"`
	DeviceName string     `json:"device_name"`
	IssuedVia  string     `json:"issued_via"`
	NotBefore  time.Time  `json:"not_before"`
	NotAfter   time.Time  `json:"not_after"`
	RevokedAt  *time.Time `json:"revoked_at"`
	Current    bool       `json:"current"`
}

func (a *API) login(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	id := identity(r)
	s, err := a.cfg.Auth.Login(r.Context(), auth.LoginInput{Username: in.Username, Password: in.Password, CertSerial: id.Serial, Remote: id.Remote})
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: s.Secret, Path: "/", HttpOnly: true, Secure: true,
		SameSite: http.SameSiteStrictMode, MaxAge: int(auth.SessionMax / time.Second)})
	writeJSON(w, http.StatusOK, map[string]any{"user": toUserJSON(s.User), "csrf_token": s.CSRF})
	return nil
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) error {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return auth.ErrUnauthenticated
	}
	if err := a.cfg.Auth.Logout(r.Context(), c.Value); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: true,
		SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) me(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, http.StatusOK, toUserJSON(user(r)))
	return nil
}

func (a *API) listTokens(w http.ResponseWriter, r *http.Request) error {
	tokens, err := a.cfg.Auth.ListTokens(r.Context(), user(r))
	if err != nil {
		return err
	}
	out := make([]tokenJSON, len(tokens))
	for i, t := range tokens {
		out[i] = toTokenJSON(t)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
	return nil
}

func (a *API) createToken(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	t, secret, err := a.cfg.Auth.CreateToken(r.Context(), user(r), in.Name)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": toTokenJSON(t), "secret": secret})
	return nil
}

func (a *API) revokeToken(w http.ResponseWriter, r *http.Request) error {
	if err := a.cfg.Auth.RevokeToken(r.Context(), user(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) listCertificates(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	certs, err := a.cfg.Auth.ListCertificates(r.Context(), u, u.ID)
	if err != nil {
		return err
	}
	current := identity(r).Serial
	out := make([]certificateJSON, len(certs))
	for i, c := range certs {
		out[i] = certificateJSON{Serial: c.Serial, DeviceName: c.DeviceName, IssuedVia: c.IssuedVia,
			NotBefore: c.NotBefore, NotAfter: c.NotAfter, RevokedAt: c.RevokedAt, Current: c.Serial == current}
	}
	writeJSON(w, http.StatusOK, map[string]any{"certificates": out})
	return nil
}

func (a *API) revokeCertificate(w http.ResponseWriter, r *http.Request) error {
	if err := a.cfg.Auth.RevokeCertificate(r.Context(), user(r), r.PathValue("serial")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// renewCertificate renews the certificate of the connection itself: the
// serial comes from the TLS handshake, never from the request.
func (a *API) renewCertificate(w http.ResponseWriter, r *http.Request) error {
	serial := identity(r).Serial
	if serial == "" {
		return fmt.Errorf("%w: renewal needs a client certificate on the connection", auth.ErrForbidden)
	}
	var in struct {
		CSR string `json:"csr"` // base64 of the DER request
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	der, err := base64.StdEncoding.DecodeString(in.CSR)
	if err != nil {
		return fmt.Errorf("%w: csr must be base64", errBadRequest)
	}
	cert, err := a.cfg.Auth.RenewCertificate(r.Context(), user(r), serial, der)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"certificate": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})),
		"serial":      pki.SerialString(cert.SerialNumber),
		"not_after":   cert.NotAfter,
	})
	return nil
}
