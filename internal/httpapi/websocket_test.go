package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/collab"
	"github.com/0xmhha/garret/internal/discussion"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/inbox"
	"github.com/0xmhha/garret/internal/realtime"
)

type wsEnv struct {
	srv    *httptest.Server
	hub    *realtime.Hub
	cookie string
	docID  string
}

func newWSEnv(t *testing.T) *wsEnv {
	t.Helper()
	e, _ := fullEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	hub := realtime.NewHub(slog.New(slog.DiscardHandler))
	ids, _ := id.NewGenerator(wall{}, rand.Reader)
	co := collab.New(collab.Config{DB: e.storeDB, IDs: ids, Clock: wall{}, Notifier: inbox.NewNotifier(ids, wall{}), Events: hub,
		Log: slog.New(slog.DiscardHandler), Quiet: time.Hour, Grace: time.Hour})
	done := make(chan struct{}, 2)
	go func() { hub.Run(ctx); done <- struct{}{} }()
	go func() { co.Run(ctx); done <- struct{}{} }()
	cfg := e.api.cfg
	cfg.Hub, cfg.Collab = hub, co
	api := New(cfg)
	d, err := discussion.New(e.storeDB, audit.NewRecorder(ids, wall{}), ids, wall{}, nopEvents{}).Create(ctx, e.admin.User, "libfoo", "Plan")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api)
	t.Cleanup(func() { srv.Close(); cancel(); <-done; <-done })
	c := &client{e: &env{api: api}}
	c.login(t)
	return &wsEnv{srv: srv, hub: hub, cookie: c.cookie.Name + "=" + c.cookie.Value, docID: d.BodyDocID}
}

func (e *wsEnv) dial(t *testing.T, path, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{"Cookie": {e.cookie}, "Origin": {origin}}
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+path, &websocket.DialOptions{HTTPHeader: h})
}

func TestEventsWebSocket(t *testing.T) {
	e := newWSEnv(t)
	conn, _, err := e.dial(t, basePath+"/ws", origin) //nolint:bodyclose // the dial response belongs to the upgraded connection
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	deadline := time.Now().Add(5 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := make(chan realtime.Event, 1)
	go func() {
		_, msg, err := conn.Read(ctx)
		if err == nil {
			var ev realtime.Event
			_ = json.Unmarshal(msg, &ev)
			got <- ev
		}
	}()
	for time.Now().Before(deadline) {
		e.hub.Publish(realtime.Event{Type: "comment.created", Project: "libfoo", ID: "C1"})
		select {
		case ev := <-got:
			if ev.Type != "comment.created" || ev.ID != "C1" {
				t.Fatalf("event %+v", ev)
			}
			return
		case <-time.After(50 * time.Millisecond): // the subscription may not be registered yet; publish again
		}
	}
	t.Fatal("no event received")
}

func TestWebSocketRejectsForeignOrigin(t *testing.T) {
	e := newWSEnv(t)
	_, resp, err := e.dial(t, basePath+"/ws", "https://evil.example") //nolint:bodyclose // the library closes it on a failed dial
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin: %v %v", resp, err)
	}
}

func TestCollabWebSocket(t *testing.T) {
	e := newWSEnv(t)
	path := basePath + "/projects/libfoo/collab/" + e.docID
	conn, _, err := e.dial(t, path+"?generation=1", origin) //nolint:bodyclose // the dial response belongs to the upgraded connection
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, msg, err := conn.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || len(msg) == 0 || msg[0] != 0 {
		t.Fatalf("first message %v %v %v", typ, msg, err)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(ctx); websocket.CloseStatus(err) != websocket.StatusUnsupportedData {
		t.Fatalf("text frame: %v", err)
	}
	if _, _, err := e.dial(t, path+"?generation=7", origin); err == nil { //nolint:bodyclose // the library closes it on a failed dial
		t.Fatal("stale generation accepted")
	}
	if _, _, err := e.dial(t, path, origin); err == nil { //nolint:bodyclose // the library closes it on a failed dial
		t.Fatal("missing generation accepted")
	}
	big := bytes.Repeat([]byte{0}, readLimit+10)
	conn2, _, err := e.dial(t, path+"?generation=1", origin) //nolint:bodyclose // the dial response belongs to the upgraded connection
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn2.CloseNow() }()
	_, _, _ = conn2.Read(ctx)
	_ = conn2.Write(ctx, websocket.MessageBinary, big)
	if _, _, err := conn2.Read(ctx); websocket.CloseStatus(err) != websocket.StatusMessageTooBig {
		t.Fatalf("oversized message: %v", err)
	}
}
