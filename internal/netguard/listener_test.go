package netguard

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInterfacesIncludesLoopback(t *testing.T) {
	ifaces, err := Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, ifc := range ifaces {
		if ifc.Loopback {
			for _, p := range ifc.Prefixes {
				if p.Addr().IsLoopback() && p.IsValid() {
					return
				}
			}
		}
	}
	t.Fatalf("no loopback interface with a loopback prefix in %+v", ifaces)
}

// fakeConn is a net.Conn with a chosen remote address.
type fakeConn struct {
	net.Conn
	remote net.Addr
	mu     sync.Mutex
	closed bool
}

func (c *fakeConn) RemoteAddr() net.Addr { return c.remote }
func (c *fakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}
func (c *fakeConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// fakeListener hands out the queued connections, then errClosed.
type fakeListener struct {
	conns []net.Conn
}

var errClosed = errors.New("listener closed")

func (l *fakeListener) Accept() (net.Conn, error) {
	if len(l.conns) == 0 {
		return nil, errClosed
	}
	c := l.conns[0]
	l.conns = l.conns[1:]
	return c, nil
}
func (l *fakeListener) Close() error   { return nil }
func (l *fakeListener) Addr() net.Addr { return &net.TCPAddr{} }

func tcp(s string) net.Addr { return net.TCPAddrFromAddrPort(netip.MustParseAddrPort(s)) }

func TestListenerFilters(t *testing.T) {
	outside := &fakeConn{remote: tcp("192.168.2.9:5000")}
	nonTCP := &fakeConn{remote: &net.UnixAddr{Name: "/tmp/x", Net: "unix"}}
	inside := &fakeConn{remote: tcp("192.168.1.9:5000")}
	inner := &fakeListener{conns: []net.Conn{outside, nonTCP, inside}}
	var logs bytes.Buffer
	l := Listener(inner, []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}, slog.New(slog.NewTextHandler(&logs, nil)))

	got, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if got != inside {
		t.Fatalf("Accept returned %v, want the allowed connection", got.RemoteAddr())
	}
	if !outside.isClosed() || !nonTCP.isClosed() {
		t.Fatal("rejected connections were not closed")
	}
	if inside.isClosed() {
		t.Fatal("allowed connection was closed")
	}
	if !strings.Contains(logs.String(), "192.168.2.9") {
		t.Fatalf("rejection not logged with its address: %s", logs.String())
	}
	if _, err := l.Accept(); !errors.Is(err, errClosed) {
		t.Fatalf("err = %v, want the inner error", err)
	}
}

func TestListenerRealLoopback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inner, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := Listener(inner, nil, slog.New(slog.DiscardHandler))
	defer func() { _ = l.Close() }()
	done := make(chan error, 1)
	go func() {
		c, err := l.Accept()
		if err == nil {
			_ = c.Close()
		}
		done <- err
	}()
	c, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if err := <-done; err != nil {
		t.Fatalf("loopback connection rejected: %v", err)
	}
}
