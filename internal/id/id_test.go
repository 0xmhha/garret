package id

import (
	"bytes"
	"errors"
	"io"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/0xmhha/garret/internal/clock"
)

// fakeClock returns the time it holds; tests move it explicitly.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// seeded returns a deterministic entropy stream. MonotonicEntropy loops on a
// constant stream, so tests must not use a fixed byte pattern alone.
func seeded() io.Reader {
	return rand.NewChaCha8([32]byte{1})
}

var start = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

func newGen(t *testing.T, c clock.Clock, r io.Reader) *Generator {
	t.Helper()
	g, err := NewGenerator(c, r)
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}
	return g
}

func mustNew(t *testing.T, g *Generator) ulid.ULID {
	t.Helper()
	s, err := g.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	u, err := ulid.ParseStrict(s)
	if err != nil {
		t.Fatalf("New returned %q, not a ULID: %v", s, err)
	}
	return u
}

func TestNewGeneratorRejectsNil(t *testing.T) {
	tests := []struct {
		name string
		c    clock.Clock
		r    io.Reader
	}{
		{"nil clock", nil, seeded()},
		{"nil entropy", &fakeClock{now: start}, nil},
		{"both nil", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewGenerator(tt.c, tt.r); !errors.Is(err, ErrNilDependency) {
				t.Fatalf("err = %v, want ErrNilDependency", err)
			}
		})
	}
}

func TestNewOrdering(t *testing.T) {
	tests := []struct {
		name     string
		second   time.Time
		wantTime time.Time
	}{
		{"same millisecond", start, start},
		{"clock moves forward", start.Add(time.Second), start.Add(time.Second)},
		{"clock moves backwards", start.Add(-time.Second), start},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &fakeClock{now: start}
			g := newGen(t, c, seeded())
			first := mustNew(t, g)
			c.set(tt.second)
			second := mustNew(t, g)
			if second.Compare(first) <= 0 {
				t.Fatalf("second ID %s does not sort after first %s", second, first)
			}
			if got := ulid.Time(second.Time()); !got.Equal(tt.wantTime) {
				t.Fatalf("second ID time = %v, want %v", got, tt.wantTime)
			}
		})
	}
}

func TestNewFormat(t *testing.T) {
	g := newGen(t, &fakeClock{now: start}, seeded())
	s, err := g.New()
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != ulid.EncodedSize {
		t.Fatalf("len(%q) = %d, want %d", s, len(s), ulid.EncodedSize)
	}
}

func TestNewBeforeEpochUsesZero(t *testing.T) {
	g := newGen(t, &fakeClock{now: time.Unix(-10, 0)}, seeded())
	if got := mustNew(t, g).Time(); got != 0 {
		t.Fatalf("timestamp = %d, want 0", got)
	}
}

// failingReader fails while fail is set, then reads from next.
type failingReader struct {
	fail bool
	next io.Reader
}

var errEntropy = errors.New("entropy unavailable")

func (r *failingReader) Read(p []byte) (int, error) {
	if r.fail {
		return 0, errEntropy
	}
	return r.next.Read(p)
}

func TestNewEntropyFailureRecovers(t *testing.T) {
	r := &failingReader{fail: true, next: seeded()}
	g := newGen(t, &fakeClock{now: start}, r)
	if _, err := g.New(); !errors.Is(err, errEntropy) {
		t.Fatalf("err = %v, want wrapped entropy error", err)
	}
	r.fail = false
	mustNew(t, g)
}

func TestNewMonotonicOverflow(t *testing.T) {
	// The first ID takes all-ones entropy; any increment in the same
	// millisecond overflows 80 bits.
	r := io.MultiReader(bytes.NewReader(bytes.Repeat([]byte{0xff}, 10)), seeded())
	g := newGen(t, &fakeClock{now: start}, r)
	mustNew(t, g)
	if _, err := g.New(); !errors.Is(err, ulid.ErrMonotonicOverflow) {
		t.Fatalf("err = %v, want ErrMonotonicOverflow", err)
	}
}

func TestNewConcurrent(t *testing.T) {
	const goroutines, perGoroutine = 8, 125
	g := newGen(t, &fakeClock{now: start}, seeded())
	ids := make(chan string, goroutines*perGoroutine)
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range perGoroutine {
				s, err := g.New()
				if err != nil {
					t.Error(err)
					return
				}
				ids <- s
			}
		})
	}
	wg.Wait()
	close(ids)
	seen := make(map[string]bool, goroutines*perGoroutine)
	for s := range ids {
		if seen[s] {
			t.Fatalf("duplicate ID %s", s)
		}
		seen[s] = true
	}
	if len(seen) != goroutines*perGoroutine {
		t.Fatalf("got %d IDs, want %d", len(seen), goroutines*perGoroutine)
	}
}
