package httpapi

import (
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/pki"
)

// EnrollConfig holds the enrollment listener's dependencies.
type EnrollConfig struct {
	Auth *auth.Service
	CA   *pki.CA
	Log  *slog.Logger
	// MainURL is the main service's URL, returned so the client can
	// configure Git and the browser (for example https://host:8443).
	MainURL string
	// OnEnrolled lets the server re-check whether invitations remain open.
	OnEnrolled func()
}

// NewEnrollHandler returns the enrollment listener's handler. It serves
// exactly GET /ca.crt and POST /enroll; no route of the main API is
// reachable from it (transport.md 2.2).
func NewEnrollHandler(cfg EnrollConfig) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ca.crt", func(w http.ResponseWriter, _ *http.Request) {
		setSecurityHeaders(w.Header())
		w.Header().Set("Content-Type", "application/x-pem-file")
		_, _ = w.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cfg.CA.Cert.Raw}))
	})
	mux.HandleFunc("POST /enroll", func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w.Header())
		if err := enroll(w, r, cfg); err != nil {
			writeError(w, r, cfg.Log, err)
		}
	})
	return mux
}

func enroll(w http.ResponseWriter, r *http.Request, cfg EnrollConfig) error {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return auth.ErrInvalidInvitation
	}
	var in struct {
		Code        string `json:"code"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
		Device      string `json:"device"`
		CSR         string `json:"csr"` // base64 of the DER request
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	der, err := base64.StdEncoding.DecodeString(in.CSR)
	if err != nil {
		return fmt.Errorf("%w: csr must be base64", errBadRequest)
	}
	issued, err := cfg.Auth.Enroll(r.Context(), ap.Addr().Unmap(), in.Code,
		auth.NewAccount{Username: in.Username, DisplayName: in.DisplayName, Password: in.Password, Device: in.Device}, der)
	if err != nil {
		return err
	}
	if cfg.OnEnrolled != nil {
		cfg.OnEnrolled()
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"user":        toUserJSON(issued.User),
		"certificate": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issued.Certificate.Raw})),
		"ca":          string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issued.CA.Raw})),
		"server_url":  cfg.MainURL,
	})
	return nil
}
