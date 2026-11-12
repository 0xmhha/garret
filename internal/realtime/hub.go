package realtime

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

// DefaultQueue is the capacity of the hub's inbound event queue.
const DefaultQueue = 1024

// Event tells a browser which resource changed; the browser re-fetches it.
type Event struct {
	Type    string `json:"type"`
	Project string `json:"project"`
	Review  int    `json:"review,omitempty"`
	ID      string `json:"id,omitempty"`
}

// Hub delivers events to subscriptions. Run must be running for delivery.
// All methods are safe for concurrent use.
type Hub struct {
	log        *slog.Logger
	events     chan Event
	register   chan *Subscription
	unregister chan *Subscription
	closeUser  chan string
	done       chan struct{}
	dropped    atomic.Int64
}

// Option configures NewHub.
type Option func(*Hub)

// WithQueue sets the inbound queue capacity.
func WithQueue(n int) Option { return func(h *Hub) { h.events = make(chan Event, n) } }

// NewHub returns a hub; start it with Run.
func NewHub(log *slog.Logger, opts ...Option) *Hub {
	h := &Hub{
		log:        log,
		events:     make(chan Event, DefaultQueue),
		register:   make(chan *Subscription),
		unregister: make(chan *Subscription),
		closeUser:  make(chan string),
		done:       make(chan struct{}),
	}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Subscription receives the events its user may see. The hub closes the
// channel returned by Events when the subscription ends.
type Subscription struct {
	hub    *Hub
	user   string
	canSee func(project string) bool
	ch     chan Event
	once   sync.Once
}

// Events returns the subscription's event channel.
func (s *Subscription) Events() <-chan Event { return s.ch }

// Close ends the subscription. Calling it more than once has no effect.
func (s *Subscription) Close() {
	s.once.Do(func() {
		select {
		case s.hub.unregister <- s:
		case <-s.hub.done:
		}
	})
}

// Subscribe registers a subscription for user with a buffer of buf events.
// canSee decides which projects' events reach it. If the hub has stopped,
// the returned subscription is already closed.
func (h *Hub) Subscribe(ctx context.Context, user string, canSee func(project string) bool, buf int) *Subscription {
	s := &Subscription{hub: h, user: user, canSee: canSee, ch: make(chan Event, max(1, buf))}
	select {
	case h.register <- s:
	case <-h.done:
		close(s.ch)
	case <-ctx.Done():
		close(s.ch)
	}
	return s
}

// Publish queues e without blocking. When the queue is full the event is
// dropped and counted; clients re-fetch on their next view, so a lost hint
// never leaves stale state.
func (h *Hub) Publish(e Event) {
	select {
	case h.events <- e:
	default:
		h.dropped.Add(1)
	}
}

// Dropped returns how many events were dropped because the queue was full.
func (h *Hub) Dropped() int64 { return h.dropped.Load() }

// CloseUser ends every subscription of user, for example after the user is
// disabled.
func (h *Hub) CloseUser(user string) {
	select {
	case h.closeUser <- user:
	case <-h.done:
	}
}

// Run delivers events until ctx is done, then closes every subscription.
// Only Run closes subscription channels.
func (h *Hub) Run(ctx context.Context) {
	subs := map[*Subscription]struct{}{}
	end := func(s *Subscription) {
		if _, ok := subs[s]; ok {
			delete(subs, s)
			close(s.ch)
		}
	}
	defer func() {
		for s := range subs {
			end(s)
		}
		close(h.done)
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case s := <-h.register:
			subs[s] = struct{}{}
		case s := <-h.unregister:
			end(s)
		case u := <-h.closeUser:
			for s := range subs {
				if s.user == u {
					end(s)
				}
			}
		case e := <-h.events:
			for s := range subs {
				if !s.canSee(e.Project) {
					continue
				}
				select {
				case s.ch <- e:
				default:
					h.log.Info("closing a slow event subscription", "user", s.user)
					end(s)
				}
			}
		}
	}
}
