package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/collab"
)

// WebSocket limits (transport.md 5, Q3).
const (
	maxConnsPerUser = 16
	maxConnsPerDoc  = 64
	sendBuffer      = 64
	writeTimeout    = 10 * time.Second
	pingInterval    = 30 * time.Second
	readLimit       = collab.MaxUpdateBytes + 64
)

var errTooManyConnections = errors.New("httpapi: too many connections")

// connLimits counts open WebSocket connections per user and per document.
type connLimits struct {
	mu    sync.Mutex
	users map[string]int
	docs  map[string]int
}

func (l *connLimits) acquire(user, doc string) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.users == nil {
		l.users, l.docs = map[string]int{}, map[string]int{}
	}
	if l.users[user] >= maxConnsPerUser || (doc != "" && l.docs[doc] >= maxConnsPerDoc) {
		return nil, false
	}
	l.users[user]++
	if doc != "" {
		l.docs[doc]++
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.users[user]--
			if doc != "" {
				l.docs[doc]--
			}
		})
	}, true
}

// websocketOK checks what a WebSocket upgrade needs beyond authentication:
// the Origin must be the server's own, exactly, because browsers send
// cookies on cross-site WebSocket requests.
func (a *API) websocketOK(r *http.Request) error {
	if r.Header.Get("Origin") != a.cfg.Origin {
		return auth.ErrForbidden
	}
	return nil
}

func accept(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	// The exact Origin check already ran; Accept's own same-host check would
	// compare against the Host header, which adds nothing here.
	return websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
}

// stillActive reports whether the connection's certificate is still active,
// checked on every message and ping, since TLS checks it only at handshake.
func (a *API) stillActive(r *http.Request) bool {
	return !a.cfg.ClientCerts || a.cfg.Active.Active(identity(r).Serial)
}

// events streams the hub's events to the browser as JSON text messages.
func (a *API) events(w http.ResponseWriter, r *http.Request) error {
	if err := a.websocketOK(r); err != nil {
		return err
	}
	u := user(r)
	release, ok := a.limits.acquire(u.ID, "")
	if !ok {
		return errTooManyConnections
	}
	defer release()
	conn, err := accept(w, r)
	if err != nil {
		return nil // Accept already answered
	}
	defer func() { _ = conn.CloseNow() }()
	ctx := conn.CloseRead(r.Context())
	sub := a.cfg.Hub.Subscribe(ctx, u.ID, func(string) bool { return true }, sendBuffer)
	defer sub.Close()
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-sub.Events():
			if !ok {
				_ = conn.Close(websocket.StatusPolicyViolation, "event stream closed")
				return nil
			}
			msg, err := json.Marshal(ev)
			if err != nil {
				return nil
			}
			if err := write(ctx, conn, websocket.MessageText, msg); err != nil {
				return nil
			}
		case <-ping.C:
			if !a.stillActive(r) || pingConn(ctx, conn) != nil {
				return nil
			}
		}
	}
}

func write(ctx context.Context, conn *websocket.Conn, typ websocket.MessageType, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(ctx, typ, msg)
}

func pingConn(ctx context.Context, conn *websocket.Conn) error {
	ctx, cancel := context.WithTimeout(ctx, 2*pingInterval)
	defer cancel()
	return conn.Ping(ctx)
}

// wsConn adapts a WebSocket to collab.Conn: Send queues without blocking
// the document owner, and a full queue closes the connection.
type wsConn struct {
	out    chan []byte
	cancel context.CancelFunc
	once   sync.Once
}

func (c *wsConn) Send(msg []byte) error {
	select {
	case c.out <- msg:
		return nil
	default:
		c.Close()
		return errTooManyConnections
	}
}

func (c *wsConn) Close() { c.once.Do(c.cancel) }

// collabDocument serves one collaborative document over the Yjs sync
// framing. Only binary messages are accepted.
func (a *API) collabDocument(w http.ResponseWriter, r *http.Request) error {
	if err := a.websocketOK(r); err != nil {
		return err
	}
	gen, err := strconv.ParseInt(r.URL.Query().Get("generation"), 10, 64)
	if err != nil || gen < 1 {
		return fmt.Errorf("%w: generation", errBadRequest)
	}
	u, project, docID := user(r), r.PathValue("project"), r.PathValue("doc")
	current, err := a.cfg.Collab.Generation(r.Context(), project, docID)
	if err != nil {
		return err
	}
	if gen != current {
		return collab.ErrStale // 409 before the upgrade: the client reloads
	}
	release, ok := a.limits.acquire(u.ID, docID)
	if !ok {
		return errTooManyConnections
	}
	defer release()
	conn, err := accept(w, r)
	if err != nil {
		return nil
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(readLimit)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	wc := &wsConn{out: make(chan []byte, sendBuffer), cancel: cancel}
	sess, err := a.cfg.Collab.Join(ctx, u, project, docID, gen, wc)
	if err != nil {
		code := websocket.StatusPolicyViolation
		if errors.Is(err, collab.ErrStale) {
			code = websocket.StatusCode(4409) // reload: the document was rebuilt
		}
		_ = conn.Close(code, "cannot join")
		return nil
	}
	defer sess.Leave()
	go func() {
		defer cancel()
		ping := time.NewTicker(pingInterval)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-wc.out:
				if write(ctx, conn, websocket.MessageBinary, msg) != nil {
					return
				}
			case <-ping.C:
				if !a.stillActive(r) || pingConn(ctx, conn) != nil {
					return
				}
			}
		}
	}()
	for {
		typ, msg, err := conn.Read(ctx)
		if err != nil {
			return nil
		}
		if typ != websocket.MessageBinary {
			_ = conn.Close(websocket.StatusUnsupportedData, "binary messages only")
			return nil
		}
		if !a.stillActive(r) || sess.Receive(ctx, msg) != nil {
			return nil
		}
	}
}

// collabInfo returns the generation a client must join with.
func (a *API) collabInfo(w http.ResponseWriter, r *http.Request) error {
	gen, err := a.cfg.Collab.Generation(r.Context(), r.PathValue("project"), r.PathValue("doc"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"generation": gen, "text_name": collab.TextName})
	return nil
}
