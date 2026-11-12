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
	ErrNotFound          = errors.New("auth: not found")
	ErrForbidden         = errors.New("auth: not allowed")
	ErrInvalid           = errors.New("auth: invalid input")
	ErrConflict          = errors.New("auth: conflict")
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
}

// NewService checks cfg and returns a Service.
func NewService(cfg Config) (*Service, error) {
	if cfg.DB == nil || cfg.Audit == nil || cfg.IDs == nil || cfg.Clock == nil || cfg.Rand == nil ||
		cfg.CA == nil || cfg.Active == nil || !cfg.Password.valid() {
		return nil, errors.New("auth: incomplete service configuration")
	}
	return &Service{
		db: cfg.DB, audit: cfg.Audit, ids: cfg.IDs, clock: cfg.Clock, rand: cfg.Rand,
		ca: cfg.CA, active: cfg.Active, password: cfg.Password,
	}, nil
}
