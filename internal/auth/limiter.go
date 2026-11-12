package auth

import (
	"sync"
	"time"

	"github.com/0xmhha/garret/internal/clock"
)

// Login and token rate limits (docs/specs/services.md, Q1).
const (
	maxFailures    = 10
	failureWindow  = 15 * time.Minute
	maxTrackedKeys = 10000
)

// limiter counts failures per key in fixed windows. Keys are "user:<name>"
// and "addr:<ip>". It is safe for concurrent use.
type limiter struct {
	clock clock.Clock
	mu    sync.Mutex
	keys  map[string]*window
}

type window struct {
	start    time.Time
	failures int
}

func newLimiter(c clock.Clock) *limiter {
	return &limiter{clock: c, keys: make(map[string]*window)}
}

// blocked reports whether any of keys has reached the limit in its window.
func (l *limiter) blocked(keys ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	for _, k := range keys {
		if w := l.current(k, now); w != nil && w.failures >= maxFailures {
			return true
		}
	}
	return false
}

// fail counts one failure for each key.
func (l *limiter) fail(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	for _, k := range keys {
		w := l.current(k, now)
		if w == nil {
			l.makeRoom(now)
			w = &window{start: now}
			l.keys[k] = w
		}
		w.failures++
	}
}

// reset forgets key, after a successful login for that username.
func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.keys, key)
}

func (l *limiter) current(k string, now time.Time) *window {
	w := l.keys[k]
	if w != nil && now.Sub(w.start) >= failureWindow {
		delete(l.keys, k)
		return nil
	}
	return w
}

// makeRoom drops expired windows, then the oldest one, when the map is full,
// so memory stays bounded however many addresses or names are tried.
func (l *limiter) makeRoom(now time.Time) {
	if len(l.keys) < maxTrackedKeys {
		return
	}
	var oldestKey string
	var oldest time.Time
	for k, w := range l.keys {
		if now.Sub(w.start) >= failureWindow {
			delete(l.keys, k)
			continue
		}
		if oldestKey == "" || w.start.Before(oldest) {
			oldestKey, oldest = k, w.start
		}
	}
	if len(l.keys) >= maxTrackedKeys {
		delete(l.keys, oldestKey)
	}
}

func (l *limiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.keys)
}
