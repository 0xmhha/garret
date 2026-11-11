package store

import (
	"context"
	"sync"

	"golang.org/x/sync/semaphore"
)

// KeyedMutex serializes callers per key (docs/specs/data.md section 7.2).
// Entries are reference-counted and removed when unused. The zero value is
// ready to use; it must not be copied after first use.
type KeyedMutex struct {
	mu      sync.Mutex
	entries map[string]*keyEntry
}

type keyEntry struct {
	token chan struct{} // holds one value while the key is locked
	refs  int
}

// Lock waits until key is free or ctx is done. It returns a function that
// unlocks; calling it more than once has no further effect.
func (m *KeyedMutex) Lock(ctx context.Context, key string) (func(), error) {
	m.mu.Lock()
	if m.entries == nil {
		m.entries = make(map[string]*keyEntry)
	}
	e := m.entries[key]
	if e == nil {
		e = &keyEntry{token: make(chan struct{}, 1)}
		m.entries[key] = e
	}
	e.refs++
	m.mu.Unlock()

	select {
	case e.token <- struct{}{}:
	case <-ctx.Done():
		m.release(key, e)
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-e.token
			m.release(key, e)
		})
	}, nil
}

func (m *KeyedMutex) release(key string, e *keyEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e.refs--
	if e.refs == 0 {
		delete(m.entries, key)
	}
}

func (m *KeyedMutex) size() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

// exclusiveWeight is taken by Exclusive; Shared takes 1. The semaphore is
// first in, first out, so a waiting Exclusive blocks later Shared callers.
const exclusiveWeight = 1 << 30

// ProjectLocks is a reader-writer lock per project: ordinary writes take it
// shared and a purge takes it exclusively. The zero value is ready to use.
// Entries are kept for the life of the server; projects are few.
type ProjectLocks struct {
	mu    sync.Mutex
	locks map[string]*semaphore.Weighted
}

// Shared waits for a shared hold on project, or for ctx to be done.
func (p *ProjectLocks) Shared(ctx context.Context, project string) (func(), error) {
	return p.acquire(ctx, project, 1)
}

// Exclusive waits until no one else holds project, or for ctx to be done.
func (p *ProjectLocks) Exclusive(ctx context.Context, project string) (func(), error) {
	return p.acquire(ctx, project, exclusiveWeight)
}

func (p *ProjectLocks) acquire(ctx context.Context, project string, n int64) (func(), error) {
	p.mu.Lock()
	if p.locks == nil {
		p.locks = make(map[string]*semaphore.Weighted)
	}
	s := p.locks[project]
	if s == nil {
		s = semaphore.NewWeighted(exclusiveWeight)
		p.locks[project] = s
	}
	p.mu.Unlock()
	if err := s.Acquire(ctx, n); err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { s.Release(n) }) }, nil
}
