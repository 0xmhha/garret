package realtime

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func runHub(t *testing.T, opts ...Option) *Hub {
	t.Helper()
	h := NewHub(slog.New(slog.DiscardHandler), opts...)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return h
}

func all(string) bool { return true }

func recv(t *testing.T, s *Subscription) (Event, bool) {
	t.Helper()
	select {
	case e, ok := <-s.Events():
		return e, ok
	case <-time.After(2 * time.Second):
		t.Fatal("no event within 2 s")
		return Event{}, false
	}
}

func TestPublishDelivers(t *testing.T) {
	h := runHub(t)
	a := h.Subscribe(context.Background(), "U1", all, 4)
	b := h.Subscribe(context.Background(), "U2", all, 4)
	h.Publish(Event{Type: "comment.created", Project: "garret", Review: 12, ID: "C1"})
	for _, s := range []*Subscription{a, b} {
		if e, ok := recv(t, s); !ok || e.ID != "C1" || e.Review != 12 {
			t.Fatalf("got %+v, %v", e, ok)
		}
	}
}

func TestPublishFiltersByVisibility(t *testing.T) {
	h := runHub(t)
	hidden := h.Subscribe(context.Background(), "U1", func(p string) bool { return p != "secret" }, 4)
	h.Publish(Event{Type: "comment.created", Project: "secret", ID: "C1"})
	h.Publish(Event{Type: "comment.created", Project: "open", ID: "C2"})
	if e, _ := recv(t, hidden); e.ID != "C2" {
		t.Fatalf("got %+v, want only C2", e)
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	h := runHub(t)
	slow := h.Subscribe(context.Background(), "U1", all, 1)
	fast := h.Subscribe(context.Background(), "U2", all, 8)
	for i := range 3 {
		h.Publish(Event{Type: "x.y", Project: "p", ID: string(rune('a' + i))})
	}
	for range 3 {
		if _, ok := recv(t, fast); !ok {
			t.Fatal("fast subscriber lost events")
		}
	}
	if _, ok := recv(t, slow); !ok {
		t.Fatal("slow subscriber should still get the event that fit")
	}
	if _, ok := recv(t, slow); ok {
		t.Fatal("slow subscriber was not closed after its buffer filled")
	}
}

func TestCloseAndCloseUser(t *testing.T) {
	h := runHub(t)
	a := h.Subscribe(context.Background(), "U1", all, 4)
	b := h.Subscribe(context.Background(), "U1", all, 4)
	c := h.Subscribe(context.Background(), "U2", all, 4)
	a.Close()
	a.Close()
	if _, ok := recv(t, a); ok {
		t.Fatal("closed subscription still open")
	}
	h.CloseUser("U1")
	if _, ok := recv(t, b); ok {
		t.Fatal("CloseUser left a subscription of the user open")
	}
	h.Publish(Event{Type: "x.y", Project: "p", ID: "1"})
	if _, ok := recv(t, c); !ok {
		t.Fatal("another user's subscription was closed")
	}
}

func TestPublishNeverBlocks(t *testing.T) {
	h := NewHub(slog.New(slog.DiscardHandler), WithQueue(1)) // not running: nothing drains the queue
	done := make(chan struct{})
	go func() {
		for range 100 {
			h.Publish(Event{Type: "x.y", Project: "p"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked")
	}
	if h.Dropped() < 99 {
		t.Fatalf("dropped %d, want at least 99", h.Dropped())
	}
}

func TestSubscriptionsCloseOnShutdown(t *testing.T) {
	h := NewHub(slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.Run(ctx); close(done) }()
	s := h.Subscribe(context.Background(), "U1", all, 4)
	cancel()
	<-done
	if _, ok := recv(t, s); ok {
		t.Fatal("subscription open after the hub stopped")
	}
}
