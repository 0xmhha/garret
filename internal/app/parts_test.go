package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/0xmhha/garret/internal/clock"
	"github.com/0xmhha/garret/internal/netguard"
)

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestListenAddrs(t *testing.T) {
	lan := netip.MustParsePrefix("192.168.1.0/24")
	ifaces := []netguard.Interface{
		{Name: "lo0", Up: true, Loopback: true, Prefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8")}},
		{Name: "en1", Up: true, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.30/24"), netip.MustParsePrefix("fe80::1/64")}},
		{Name: "en0", Up: true, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.20/24")}},
		{Name: "en2", Up: false, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.40/24")}},
		{Name: "en3", Up: true, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.5/8")}},
	}
	got, err := ListenAddrs([]netip.Prefix{lan}, ifaces)
	want := []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("192.168.1.20"), netip.MustParseAddr("192.168.1.30")}
	if err != nil || len(got) != len(want) {
		t.Fatalf("ListenAddrs = %v, %v", got, err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ListenAddrs = %v, want %v", got, want)
		}
	}
	if _, err := ListenAddrs([]netip.Prefix{netip.MustParsePrefix("172.16.0.0/12")}, ifaces); !errors.Is(err, ErrNoAddress) {
		t.Fatalf("no address in network: %v", err)
	}
}

func TestEvery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		var n atomic.Int32
		done := make(chan struct{})
		go func() { every(ctx, time.Minute, func(context.Context) { n.Add(1) }); close(done) }()
		time.Sleep(3*time.Minute + time.Second)
		synctest.Wait()
		if got := n.Load(); got != 3 {
			t.Fatalf("ran %d times in 3 intervals", got)
		}
		cancel()
		<-done
	})
}

// fakeListeners records opens and closes of the enrollment listeners.
type fakeListeners struct {
	mu            sync.Mutex
	open          bool
	opens, closes int
	failOpen      bool
}

func (f *fakeListeners) Open() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOpen {
		return errors.New("address in use")
	}
	f.open = true
	f.opens++
	return nil
}

func (f *fakeListeners) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.open = false
	f.closes++
	return nil
}

func (f *fakeListeners) state() (bool, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.open, f.opens, f.closes
}

// invitations is a fake OpenInvitationsUntil.
type invitations struct {
	mu    sync.Mutex
	until time.Time
	open  bool
	err   error
	calls int
}

func (i *invitations) set(until time.Time, open bool, err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.until, i.open, i.err = until, open, err
}

func (i *invitations) check(context.Context) (time.Time, bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.calls++
	return i.until, i.open, i.err
}

func TestGate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inv := &invitations{}
		l := &fakeListeners{}
		g := newGate(inv.check, l, clock.System{}, discard())
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { g.Run(ctx); close(done) }()

		synctest.Wait()
		if open, _, _ := l.state(); open {
			t.Fatal("open without invitations")
		}
		inv.set(time.Now().Add(30*time.Minute), true, nil)
		g.Changed()
		synctest.Wait()
		if open, opens, _ := l.state(); !open || opens != 1 {
			t.Fatal("not opened after an invitation was created")
		}
		g.Changed() // still open: no second open
		synctest.Wait()
		if _, opens, _ := l.state(); opens != 1 {
			t.Fatalf("opened %d times", opens)
		}
		// The invitation expires without any change signal.
		inv.set(time.Time{}, false, nil)
		time.Sleep(30*time.Minute + time.Second)
		synctest.Wait()
		if open, _, closes := l.state(); open || closes != 1 {
			t.Fatal("not closed at the earliest expiry")
		}
		// A failing check keeps the state and retries.
		inv.set(time.Time{}, false, errors.New("database is locked"))
		g.Changed()
		synctest.Wait()
		inv.mu.Lock()
		before := inv.calls
		inv.mu.Unlock()
		time.Sleep(retryAfter + time.Second)
		synctest.Wait()
		inv.mu.Lock()
		after := inv.calls
		inv.mu.Unlock()
		if after <= before {
			t.Fatal("failed check not retried")
		}
		// Open at shutdown: Run closes the listeners.
		inv.set(time.Now().Add(time.Hour), true, nil)
		g.Changed()
		synctest.Wait()
		cancel()
		<-done
		if open, _, _ := l.state(); open {
			t.Fatal("listeners left open after shutdown")
		}
	})
}

func TestGateChangedNeverBlocks(t *testing.T) {
	g := newGate((&invitations{}).check, &fakeListeners{}, clock.System{}, discard())
	for range 100 {
		g.Changed() // no Run: must not block
	}
}

func TestGateRetriesFailedOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inv := &invitations{}
		inv.set(time.Now().Add(time.Hour), true, nil)
		l := &fakeListeners{failOpen: true}
		g := newGate(inv.check, l, clock.System{}, discard())
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { g.Run(ctx); close(done) }()
		synctest.Wait()
		l.mu.Lock()
		l.failOpen = false
		l.mu.Unlock()
		time.Sleep(retryAfter + time.Second)
		synctest.Wait()
		if open, _, _ := l.state(); !open {
			t.Fatal("failed open not retried")
		}
		cancel()
		<-done
	})
}

func TestRouter(t *testing.T) {
	mark := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(name)) })
	}
	h := router(mark("api"), mark("git"), mark("web"))
	for path, want := range map[string]string{
		"/api/v1/me": "api", "/git/p.git/info/refs": "git", "/": "web", "/reviews/x": "web", "/apix": "web",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
		if w.Body.String() != want {
			t.Errorf("%s routed to %q, want %q", path, w.Body.String(), want)
		}
	}
}

func TestPlaceholderHasSecurityHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	placeholder().ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Security-Policy") == "" || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("placeholder %d %v", w.Code, w.Header())
	}
}
