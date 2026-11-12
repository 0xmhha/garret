package gitserver

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/gitrepo"
	"github.com/0xmhha/garret/internal/pki"
	"github.com/0xmhha/garret/internal/project"
	"github.com/0xmhha/garret/internal/review"
	"github.com/0xmhha/garret/internal/server"
)

// DefaultMaxPush is the push size limit (components.md, Q6).
const DefaultMaxPush = 500 << 20

const (
	prefix    = "/git/"
	repoSufix = ".git/"
	infoRefs  = "info/refs"
	realm     = `Basic realm="garret", charset="UTF-8"`
)

// Config holds the handler's dependencies.
type Config struct {
	Auth        *auth.Service
	Projects    *project.Service
	Reviews     *review.Service
	Runner      *gitrepo.Runner
	ClientCerts bool
	Active      *pki.ActiveSerials
	Log         *slog.Logger
	MaxPush     int64
}

// Handler serves /git/<project>.git/... with the smart HTTP protocol.
type Handler struct {
	cfg Config
}

// New returns the Git handler.
func New(cfg Config) *Handler { return &Handler{cfg: cfg} }

// ServeHTTP authenticates, validates the route and runs one exchange.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, rest, ok := splitPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	svc, advertise, status := route(r, rest)
	if status != http.StatusOK {
		http.Error(w, http.StatusText(status), status)
		return
	}
	u, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if _, err := h.cfg.Projects.Get(r.Context(), u, name); err != nil {
		http.NotFound(w, r)
		return
	}
	dir, err := h.cfg.Projects.RepoPath(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	protocol := r.Header.Get("Git-Protocol")
	w.Header().Set("Cache-Control", "no-cache")
	if advertise {
		h.advertise(w, r, svc, dir, protocol)
		return
	}
	h.exchange(w, r, u, name, svc, dir, protocol)
}

// splitPath turns /git/<name>.git/<rest> into its parts.
func splitPath(p string) (name, rest string, ok bool) {
	tail, found := strings.CutPrefix(p, prefix)
	if !found {
		return "", "", false
	}
	name, rest, found = strings.Cut(tail, repoSufix)
	if !found || gitrepo.ValidProjectName(name) != nil {
		return "", "", false
	}
	return name, rest, true
}

// route accepts only the smart protocol's three requests; the dumb
// protocol (objects/..., HEAD) is not served.
func route(r *http.Request, rest string) (gitrepo.Service, bool, int) {
	switch {
	case rest == infoRefs && r.Method == http.MethodGet:
		svc := gitrepo.Service(r.URL.Query().Get("service"))
		if svc != gitrepo.ServiceUploadPack && svc != gitrepo.ServiceReceivePack {
			return "", false, http.StatusBadRequest
		}
		return svc, true, http.StatusOK
	case r.Method == http.MethodPost && (rest == string(gitrepo.ServiceUploadPack) || rest == string(gitrepo.ServiceReceivePack)):
		return gitrepo.Service(rest), false, http.StatusOK
	}
	return "", false, http.StatusNotFound
}

// authenticate checks HTTP Basic credentials (username and personal access
// token) bound to the client certificate. Every failure gets the same 401.
func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	fail := func() (auth.User, bool) {
		w.Header().Set("WWW-Authenticate", realm)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return auth.User{}, false
	}
	id, err := server.IdentityOf(r, h.cfg.ClientCerts)
	if err != nil || (h.cfg.ClientCerts && !h.cfg.Active.Active(id.Serial)) {
		return fail()
	}
	username, token, ok := r.BasicAuth()
	if !ok {
		return fail()
	}
	u, err := h.cfg.Auth.AuthenticateToken(r.Context(), auth.TokenInput{Username: username, Token: token, CertSerial: id.Serial, Remote: id.Remote})
	if err != nil {
		return fail()
	}
	return u, true
}

func (h *Handler) advertise(w http.ResponseWriter, r *http.Request, svc gitrepo.Service, dir, protocol string) {
	w.Header().Set("Content-Type", "application/x-"+string(svc)+"-advertisement")
	// Protocol v2 has no service line (git's http-backend.c, get_info_refs).
	if !strings.Contains(protocol, "version=2") {
		_, _ = io.WriteString(w, pktLine("# service="+string(svc)+"\n")+"0000")
	}
	if err := h.cfg.Runner.Serve(r.Context(), svc, dir, true, protocol, nil, w); err != nil {
		h.cfg.Log.Warn("git advertisement failed", "service", svc, "err", err)
	}
}

func (h *Handler) exchange(w http.ResponseWriter, r *http.Request, u auth.User, name string, svc gitrepo.Service, dir, protocol string) {
	body := io.Reader(http.MaxBytesReader(w, r.Body, h.cfg.MaxPush))
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(body)
		if err != nil {
			http.Error(w, "bad gzip body", http.StatusBadRequest)
			return
		}
		defer func() { _ = gz.Close() }()
		body = io.LimitReader(gz, h.cfg.MaxPush)
	}
	w.Header().Set("Content-Type", "application/x-"+string(svc)+"-result")
	if svc == gitrepo.ServiceUploadPack {
		if err := h.cfg.Runner.Serve(r.Context(), svc, dir, false, protocol, body, w); err != nil {
			h.cfg.Log.Warn("git fetch failed", "project", name, "err", err)
		}
		return
	}
	unlock, err := h.cfg.Projects.LockRepo(r.Context(), name)
	if err != nil {
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	defer unlock()
	// From here the push must run to its end and be recorded even if the
	// client disconnects: otherwise a client could drop the connection right
	// after git moves a branch and the move would never become a revision.
	// The runner's long timeout still bounds every step.
	ctx := context.WithoutCancel(r.Context())
	before, err := h.cfg.Runner.Refs(ctx, dir)
	if err != nil {
		http.Error(w, "repository unavailable", http.StatusInternalServerError)
		return
	}
	if err := h.cfg.Runner.Serve(ctx, svc, dir, false, protocol, body, w); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.cfg.Log.Warn("push over the size limit", "project", name)
		} else {
			h.cfg.Log.Warn("git push failed", "project", name, "err", err)
		}
	}
	after, err := h.cfg.Runner.Refs(ctx, dir)
	if err != nil {
		h.cfg.Log.Error("reading refs after a push failed", "project", name, "err", err)
		return
	}
	if changes := moved(before, after); len(changes) > 0 {
		if err := h.cfg.Reviews.HandlePush(ctx, name, u, changes); err != nil {
			h.cfg.Log.Error("recording a push failed", "project", name, "err", err)
		}
	}
}

// moved lists branches whose head changed, in name order.
func moved(before, after map[string]string) []review.RefChange {
	var out []review.RefChange
	for ref, sha := range after {
		if before[ref] != sha {
			out = append(out, review.RefChange{Ref: ref, Old: before[ref], New: sha})
		}
	}
	for ref, sha := range before {
		if _, ok := after[ref]; !ok {
			out = append(out, review.RefChange{Ref: ref, Old: sha, New: strings.Repeat("0", len(sha))})
		}
	}
	slices.SortFunc(out, func(a, b review.RefChange) int { return strings.Compare(a.Ref, b.Ref) })
	return out
}

func pktLine(s string) string { return fmt.Sprintf("%04x%s", len(s)+4, s) }
