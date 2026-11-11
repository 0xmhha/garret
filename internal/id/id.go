package id

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/oklog/ulid/v2"

	"github.com/0xmhha/garret/internal/clock"
)

// ErrNilDependency is returned by NewGenerator when the clock or the entropy
// source is nil.
var ErrNilDependency = errors.New("id: nil clock or entropy source")

// Generator creates ULIDs that never sort before an earlier ULID from the
// same Generator, even when the clock moves backwards. It is safe for
// concurrent use.
type Generator struct {
	clock clock.Clock

	mu      sync.Mutex
	entropy *ulid.MonotonicEntropy
	lastMS  uint64
}

// NewGenerator returns a Generator that reads time from c and randomness from
// entropy. Production code passes crypto/rand.Reader. It returns
// ErrNilDependency if either argument is nil.
func NewGenerator(c clock.Clock, entropy io.Reader) (*Generator, error) {
	if c == nil || entropy == nil {
		return nil, ErrNilDependency
	}
	// An increment bound of 0 selects the library's secure default.
	return &Generator{clock: c, entropy: ulid.Monotonic(entropy, 0)}, nil
}

// New returns a new ULID as its 26-character string form. It returns an
// error if the entropy source fails or if more IDs are requested within one
// millisecond than the monotonic counter can hold; the generator stays usable
// after either error.
func (g *Generator) New() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// A time before the Unix epoch would wrap around in ulid.Timestamp.
	ms := uint64(0)
	if now := g.clock.Now(); now.UnixMilli() > 0 {
		ms = ulid.Timestamp(now)
	}
	// Reusing the last timestamp keeps IDs ordered when the clock goes back.
	ms = max(ms, g.lastMS)

	u, err := ulid.New(ms, g.entropy)
	if err != nil {
		return "", fmt.Errorf("id: generate: %w", err)
	}
	g.lastMS = ms
	return u.String(), nil
}
