package auth

import (
	"errors"
	"io"
	"sync"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/clock"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/pki"
	"github.com/0xmhha/garret/internal/store"
)

// Service errors. Transport maps them to API error codes.
var (
	ErrNotFound  = errors.New("auth: not found")
	ErrForbidden = errors.New("auth: not allowed")
	ErrInvalid   = errors.New("auth: invalid input")
	ErrConflict  = errors.New("auth: conflict")
	// ErrRenewalRefused is returned when a certificate may not be renewed
	// now: it is not due, or it was renewed already.
	ErrRenewalRefused    = errors.New("auth: renewal refused")
	ErrInvalidInvitation = errors.New("auth: invitation is not valid")
	ErrSetupClosed       = errors.New("auth: setup is not available")
)

// Config holds the Service's dependencies.
type Config struct {
	DB       *store.DB
	Audit    *audit.Recorder
	IDs      *id.Generator
	Clock    clock.Clock
	Rand     io.Reader // crypto/rand.Reader in production
	CA       *pki.CA
	Active   *pki.ActiveSerials
	Password Params // Default in production
	// ClientCertsOff disables the binding between sessions or tokens and the
	// client certificate (--client-cert=off). The zero value keeps it on.
	ClientCertsOff bool
	// OnDisable is called with a user ID after the user is disabled, so the
	// caller can close the user's open connections. It may be nil.
	OnDisable func(userID string)
}

// Service manages users, invitations, client certificates, sessions and
// access tokens. It is safe for concurrent use.
type Service struct {
	db       *store.DB
	audit    *audit.Recorder
	ids      *id.Generator
	clock    clock.Clock
	rand     io.Reader
	ca       *pki.CA
	active   *pki.ActiveSerials
	password Params

	setupMu    sync.Mutex
	setupToken string

	clientCerts bool
	onDisable   func(string)
	limiter     *limiter
	dummyHash   string // verified for unknown users so failures take equal time
}

// NewService checks cfg and returns a Service.
func NewService(cfg Config) (*Service, error) {
	if cfg.DB == nil || cfg.Audit == nil || cfg.IDs == nil || cfg.Clock == nil || cfg.Rand == nil ||
		cfg.CA == nil || cfg.Active == nil || !cfg.Password.valid() {
		return nil, errors.New("auth: incomplete service configuration")
	}
	dummy, err := HashPassword("garret-unknown-user-placeholder", cfg.Password, cfg.Rand)
	if err != nil {
		return nil, err
	}
	onDisable := cfg.OnDisable
	if onDisable == nil {
		onDisable = func(string) {}
	}
	return &Service{
		db: cfg.DB, audit: cfg.Audit, ids: cfg.IDs, clock: cfg.Clock, rand: cfg.Rand,
		ca: cfg.CA, active: cfg.Active, password: cfg.Password,
		clientCerts: !cfg.ClientCertsOff, onDisable: onDisable, limiter: newLimiter(cfg.Clock), dummyHash: dummy,
	}, nil
}
