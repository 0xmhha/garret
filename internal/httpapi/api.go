package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/0xmhha/garret/internal/admin"
	"github.com/0xmhha/garret/internal/attachment"
	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/collab"
	"github.com/0xmhha/garret/internal/comment"
	"github.com/0xmhha/garret/internal/discussion"
	"github.com/0xmhha/garret/internal/inbox"
	"github.com/0xmhha/garret/internal/pki"
	"github.com/0xmhha/garret/internal/project"
	"github.com/0xmhha/garret/internal/realtime"
	"github.com/0xmhha/garret/internal/review"
	"github.com/0xmhha/garret/internal/server"
)

// Limits and names (transport.md 3.1).
const (
	maxJSONBody   = 1 << 20 // Q2
	sessionCookie = "__Host-garret_session"
	csrfHeader    = "X-CSRF-Token"
	basePath      = "/api/v1"
)

// Config holds the API's dependencies. Origin is the server origin that
// state-changing requests must name, for example https://review.local:8443.
type Config struct {
	Auth        *auth.Service
	Projects    *project.Service
	Reviews     *review.Service
	Comments    *comment.Service
	Discussions *discussion.Service
	Attachments *attachment.Service
	Inbox       *inbox.Service
	Admin       *admin.Service
	Hub         *realtime.Hub
	Collab      *collab.Service
	Origin      string
	ClientCerts bool
	// Active is consulted on every request: TLS checks the serial only at
	// handshake, so a connection opened before a revocation must not keep
	// working.
	Active *pki.ActiveSerials
	// EnrollURL is the enrollment listener's base URL and CAFingerprint the
	// CA's SHA-256; together they make the secret-free invitation link.
	EnrollURL     string
	CAFingerprint string
	// OnInvitationsChanged lets the server open or close the enrollment
	// listener after an invitation is created or revoked. It may be nil.
	OnInvitationsChanged func()
	Log                  *slog.Logger
}

// API is the JSON API handler.
type API struct {
	cfg    Config
	mux    *http.ServeMux
	limits connLimits
}

// New returns the API with every route registered.
func New(cfg Config) *API {
	a := &API{cfg: cfg, mux: http.NewServeMux()}
	a.routes()
	return a
}

// ServeHTTP sets the security headers on every response and dispatches.
func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w.Header())
	a.mux.ServeHTTP(w, r)
}

func setSecurityHeaders(h http.Header) {
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self'; script-src 'self'; "+
		"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cache-Control", "no-store")
}

type ctxKey int

const (
	keyUser ctxKey = iota
	keyIdentity
)

// handler is an API handler that returns an error to be mapped.
type handler func(w http.ResponseWriter, r *http.Request) error

// public wraps a handler that runs without a session, such as login. The
// transport identity is still required, and state-changing requests must
// come from the server's own origin.
func (a *API) public(h handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := server.IdentityOf(r, a.cfg.ClientCerts)
		if err != nil || (a.cfg.ClientCerts && !a.cfg.Active.Active(id.Serial)) {
			writeError(w, r, a.cfg.Log, auth.ErrUnauthenticated)
			return
		}
		if changesState(r) && r.Header.Get("Origin") != a.cfg.Origin {
			writeError(w, r, a.cfg.Log, auth.ErrForbidden)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), keyIdentity, id))
		if err := h(w, r); err != nil {
			writeError(w, r, a.cfg.Log, err)
		}
	})
}

// authed wraps a handler that needs a signed-in user. State-changing
// requests also need the CSRF header and the server's origin.
func (a *API) authed(h handler) http.Handler {
	return a.public(func(w http.ResponseWriter, r *http.Request) error {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			return auth.ErrUnauthenticated
		}
		id := identity(r)
		session, err := a.cfg.Auth.Authenticate(r.Context(), c.Value, id.Serial)
		if err != nil {
			return err
		}
		if changesState(r) && !session.CheckCSRF(r.Header.Get(csrfHeader)) {
			return auth.ErrForbidden
		}
		r = r.WithContext(context.WithValue(r.Context(), keyUser, session.User))
		return h(w, r)
	})
}

func changesState(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

func user(r *http.Request) auth.User {
	u, _ := r.Context().Value(keyUser).(auth.User)
	return u
}

func identity(r *http.Request) server.Identity {
	id, _ := r.Context().Value(keyIdentity).(server.Identity)
	return id
}

// decode reads a JSON body of at most maxJSONBody bytes into v, rejecting
// unknown fields and trailing data.
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errBodyTooLarge
		}
		return fmt.Errorf("%w: %w", errBadRequest, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing data", errBadRequest)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WithSecurityHeaders sets the headers of transport.md 3.3 on every
// response of h, for handlers served beside the API.
func WithSecurityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w.Header())
		h.ServeHTTP(w, r)
	})
}
