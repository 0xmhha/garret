package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/0xmhha/garret/internal/clock"
	"github.com/0xmhha/garret/internal/httpapi"
	"github.com/0xmhha/garret/internal/netguard"
)

// ErrNoAddress is returned when no interface has an address inside the
// allowed networks.
var ErrNoAddress = errors.New("app: no interface address inside the allowed networks")

// retryAfter is how long the enrollment gate waits after a failed check or
// a failed open before trying again.
const retryAfter = 30 * time.Second

var loopback = netip.MustParseAddr("127.0.0.1")

// ListenAddrs returns 127.0.0.1 and the addresses of up, non-loopback
// interfaces that lie inside allowed, sorted and without duplicates.
func ListenAddrs(allowed []netip.Prefix, ifaces []netguard.Interface) ([]netip.Addr, error) {
	var out []netip.Addr
	for _, ifc := range ifaces {
		if !ifc.Up || ifc.Loopback {
			continue
		}
		for _, p := range ifc.Prefixes {
			if a := p.Addr().Unmap(); netguard.Allowed(allowed, a) {
				out = append(out, a)
			}
		}
	}
	if len(out) == 0 {
		return nil, ErrNoAddress
	}
	out = append(out, loopback)
	slices.SortFunc(out, func(a, b netip.Addr) int { return a.Compare(b) })
	return slices.Compact(out), nil
}

// every runs fn every interval until ctx is done. The first run is one
// interval after the call; the caller runs anything needed at startup.
func every(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn(ctx)
		}
	}
}

// switchable is a set of listeners the gate opens and closes.
type switchable interface {
	Open() error
	Close() error
}

// gate opens the enrollment listeners while an invitation can be used
// (transport.md 2.2). Only Run touches the listeners.
type gate struct {
	check     func(context.Context) (time.Time, bool, error)
	listeners switchable
	clock     clock.Clock
	log       *slog.Logger
	changed   chan struct{}
}

func newGate(check func(context.Context) (time.Time, bool, error), l switchable, c clock.Clock, log *slog.Logger) *gate {
	return &gate{check: check, listeners: l, clock: c, log: log, changed: make(chan struct{}, 1)}
}

// Changed tells the gate that invitations changed. It never blocks: a
// signal already pending covers this one.
func (g *gate) Changed() {
	select {
	case g.changed <- struct{}{}:
	default:
	}
}

// Run keeps the listeners in step with the invitations until ctx is done,
// then closes them.
func (g *gate) Run(ctx context.Context) {
	open := false
	defer func() {
		if open {
			g.closeListeners()
		}
	}()
	for {
		wait := g.step(ctx, &open)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-g.changed:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// idle is how long the gate waits when nothing is open: only a change
// signal can open it then, so any long wait is right.
const idle = 24 * time.Hour

// step checks the invitations once, opens or closes the listeners, and
// returns how long to wait before the next check without a signal.
func (g *gate) step(ctx context.Context, open *bool) time.Duration {
	until, want, err := g.check(ctx)
	if err != nil {
		g.log.Warn("enrollment: checking invitations failed; will retry", "err", err)
		return retryAfter
	}
	switch {
	case want && !*open:
		if err := g.listeners.Open(); err != nil {
			g.log.Error("enrollment: opening the listener failed; will retry", "err", err)
			return retryAfter
		}
		*open = true
		g.log.Info("enrollment listener opened", "until", until)
	case !want && *open:
		g.closeListeners()
		*open = false
	}
	if !want {
		return idle
	}
	// The earliest expiry, plus a little so the check sees it expired.
	return max(until.Sub(g.clock.Now())+time.Second, time.Second)
}

func (g *gate) closeListeners() {
	if err := g.listeners.Close(); err != nil {
		g.log.Warn("enrollment: closing the listener", "err", err)
		return
	}
	g.log.Info("enrollment listener closed")
}

// router sends /api/ to the JSON API, /git/ to the Git endpoint and every
// other path to the web interface.
func router(api, git, web http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/"):
			api.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/git/"):
			git.ServeHTTP(w, r)
		default:
			web.ServeHTTP(w, r)
		}
	})
}

const placeholderPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>garret</title></head>
<body><p>garret is running. The web interface is not part of this build yet.</p></body></html>
`

// placeholder serves the page shown until the web interface exists.
func placeholder() http.Handler {
	return httpapi.WithSecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(placeholderPage))
	}))
}
