package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

const short = 50 * time.Millisecond

func TestKeyedMutexSameKeyWaits(t *testing.T) {
	var m KeyedMutex
	unlock, err := m.Lock(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), short)
	defer cancel()
	if _, err := m.Lock(ctx, "a"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock err = %v, want deadline", err)
	}
	other, err := m.Lock(context.Background(), "b")
	if err != nil {
		t.Fatalf("different key blocked: %v", err)
	}
	other()
	unlock()
	unlock() // a second call is a no-op
	again, err := m.Lock(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	again()
	if n := m.size(); n != 0 {
		t.Fatalf("%d entries left", n)
	}
}

func TestKeyedMutexNoLeak(t *testing.T) {
	var m KeyedMutex
	var wg sync.WaitGroup
	for i := range 1000 {
		wg.Go(func() {
			key := string(rune('a' + i%26))
			unlock, err := m.Lock(context.Background(), key)
			if err != nil {
				t.Error(err)
				return
			}
			unlock()
		})
	}
	wg.Wait()
	if n := m.size(); n != 0 {
		t.Fatalf("%d entries left", n)
	}
}

func TestKeyedMutexSerializes(t *testing.T) {
	var m KeyedMutex
	var wg sync.WaitGroup
	inside := 0
	for range 20 {
		wg.Go(func() {
			unlock, err := m.Lock(context.Background(), "k")
			if err != nil {
				t.Error(err)
				return
			}
			inside++ // the race detector flags this if two holders overlap
			inside--
			unlock()
		})
	}
	wg.Wait()
}

func TestProjectLocks(t *testing.T) {
	var p ProjectLocks
	r1, err := p.Shared(context.Background(), "P")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := p.Shared(context.Background(), "P")
	if err != nil {
		t.Fatal("two shared holders should coexist")
	}
	ctx, cancel := context.WithTimeout(context.Background(), short)
	defer cancel()
	if _, err := p.Exclusive(ctx, "P"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("exclusive err = %v, want deadline while shared held", err)
	}
	if _, err := p.Exclusive(context.Background(), "Q"); err != nil {
		t.Fatalf("other project blocked: %v", err)
	}
	r1()
	r2()

	got := make(chan func(), 1)
	go func() {
		w, err := p.Exclusive(context.Background(), "P")
		if err != nil {
			t.Error(err)
		}
		got <- w
	}()
	w := <-got
	ctx2, cancel2 := context.WithTimeout(context.Background(), short)
	defer cancel2()
	if _, err := p.Shared(ctx2, "P"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shared err = %v, want deadline while exclusive held", err)
	}
	w()
	w()
	r, err := p.Shared(context.Background(), "P")
	if err != nil {
		t.Fatal(err)
	}
	r()
}

func TestProjectLocksExclusiveBlocksNewShared(t *testing.T) {
	var p ProjectLocks
	r, err := p.Shared(context.Background(), "P")
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan func(), 1)
	go func() {
		w, err := p.Exclusive(context.Background(), "P")
		if err != nil {
			t.Error(err)
		}
		waiting <- w
	}()
	// Give the exclusive request time to queue; shared must then wait behind it.
	time.Sleep(short) // only orders the goroutines; correctness does not depend on the duration
	ctx, cancel := context.WithTimeout(context.Background(), short)
	defer cancel()
	if _, err := p.Shared(ctx, "P"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("new shared err = %v, want it queued behind exclusive", err)
	}
	r()
	w := <-waiting
	w()
}
