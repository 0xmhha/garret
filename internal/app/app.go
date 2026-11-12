package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/0xmhha/garret/internal/admin"
	"github.com/0xmhha/garret/internal/attachment"
	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/clock"
	"github.com/0xmhha/garret/internal/collab"
	"github.com/0xmhha/garret/internal/comment"
	"github.com/0xmhha/garret/internal/config"
	"github.com/0xmhha/garret/internal/datadir"
	"github.com/0xmhha/garret/internal/discussion"
	"github.com/0xmhha/garret/internal/diskcrypt"
	"github.com/0xmhha/garret/internal/gitrepo"
	"github.com/0xmhha/garret/internal/gitserver"
	"github.com/0xmhha/garret/internal/httpapi"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/inbox"
	"github.com/0xmhha/garret/internal/netguard"
	"github.com/0xmhha/garret/internal/pki"
	"github.com/0xmhha/garret/internal/project"
	"github.com/0xmhha/garret/internal/realtime"
	"github.com/0xmhha/garret/internal/review"
	"github.com/0xmhha/garret/internal/server"
	"github.com/0xmhha/garret/internal/store"
	"github.com/0xmhha/garret/internal/webui"
)

// Intervals of the background work (transport.md 2.5).
const (
	activeRefresh     = 30 * time.Second
	certificateCheck  = time.Hour
	sessionSweep      = time.Hour
	notificationSweep = 24 * time.Hour
	shutdownGrace     = 10 * time.Second
	enrollStopGrace   = 5 * time.Second
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 2 * time.Minute
	maxGitCommands    = 4
	diskProbeLimit    = 5 * time.Second
)

// System holds what the app takes from the machine. Tests replace the
// interfaces and the output.
type System struct {
	Clock      clock.Clock
	Rand       io.Reader // crypto/rand.Reader in production
	Interfaces func() ([]netguard.Interface, error)
	GitPath    string
	Log        *slog.Logger
	Out        io.Writer // the startup banner and warnings
	// Listen opens a listening socket; nil means net.ListenConfig. Tests
	// use it to present connections from other peer addresses to the
	// network filter.
	Listen func(ctx context.Context, address string) (net.Listener, error)
	// DiskProbe checks whether disk encryption appears to be on; nil skips
	// the check.
	DiskProbe *diskcrypt.Probe
	// ActiveRefresh overrides the 30-second refresh of active serials;
	// zero keeps it. Tests shorten it.
	ActiveRefresh time.Duration
}

// App is an assembled garret server. New builds it and opens the main
// listeners; Run serves until its context ends.
type App struct {
	sys     System
	cfg     config.Serve
	allowed []netip.Prefix
	db      *store.DB
	ca      *pki.CA
	certs   *pki.ServerCertificates
	auth    *auth.Service
	inbox   *inbox.Service
	hub     *realtime.Hub
	collab  *collab.Service
	gate    *gate
	mainLns []net.Listener
	main    *http.Server
	mainURL string
}

// New runs startup steps 1 to 6 of transport.md 2.5 and opens the main
// listeners without accepting on them. On error everything opened so far is
// closed.
func New(ctx context.Context, cfg config.Serve, sys System) (*App, error) {
	a := &App{sys: sys, cfg: cfg}
	if err := a.open(ctx); err != nil {
		a.closeAll()
		return nil, err
	}
	return a, nil
}

func (a *App) open(ctx context.Context) error {
	cfg, sys := a.cfg, a.sys
	ifaces, err := sys.Interfaces()
	if err != nil {
		return fmt.Errorf("app: list interfaces: %w", err)
	}
	if a.allowed, err = netguard.ResolveAllowed(cfg.Allow, ifaces); err != nil {
		return err
	}
	addrs, err := ListenAddrs(a.allowed, ifaces)
	if err != nil && (!cfg.Listen.IsValid() || !cfg.EnrollListen.IsValid()) {
		return err
	}
	dir, err := datadir.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	if a.db, err = store.Open(ctx, dir.DBPath()); err != nil {
		return err
	}
	runner, err := gitrepo.NewRunner(ctx, sys.GitPath, max(1, min(runtime.GOMAXPROCS(0)-1, maxGitCommands)), dir.Root())
	if err != nil {
		return err
	}
	names := pki.Names{Host: cfg.Host, Networks: a.allowed, Addresses: caAddresses(addrs, cfg)}
	if a.ca, err = pki.LoadOrCreateCA(dir, sys.Clock, names, sys.Rand); err != nil {
		return err
	}
	if a.certs, err = pki.NewServerCertificates(a.ca, dir, sys.Clock, names, sys.Log, sys.Rand); err != nil {
		return err
	}
	mainCert := server.GetCertificate(a.certs.GetCertificate)
	if cfg.TLSCert != "" {
		if mainCert, err = server.StaticCertificate(cfg.TLSCert, cfg.TLSKey); err != nil {
			return err
		}
	}
	if err := a.build(ctx, dir, runner, addrs, mainCert); err != nil {
		return err
	}
	a.banner()
	a.diskWarning(ctx)
	return nil
}

// build creates the services and handlers and opens the main listeners
// (steps 4 to 6).
func (a *App) build(ctx context.Context, dir *datadir.Dir, runner *gitrepo.Runner, addrs []netip.Addr, mainCert server.GetCertificate) error {
	sys, cfg := a.sys, a.cfg
	ids, err := id.NewGenerator(sys.Clock, sys.Rand)
	if err != nil {
		return err
	}
	rec := audit.NewRecorder(ids, sys.Clock)
	active := &pki.ActiveSerials{}
	a.hub = realtime.NewHub(sys.Log)
	notifier := inbox.NewNotifier(ids, sys.Clock)
	a.collab = collab.New(collab.Config{DB: a.db, IDs: ids, Clock: sys.Clock, Notifier: notifier, Events: a.hub, Log: sys.Log,
		Quiet: collab.DefaultQuiet, Grace: collab.DefaultGrace})
	if a.auth, err = auth.NewService(auth.Config{DB: a.db, Audit: rec, IDs: ids, Clock: sys.Clock, Rand: sys.Rand, CA: a.ca,
		Active: active, Password: auth.Default, ClientCertsOff: !cfg.ClientCerts,
		OnDisable: func(user string) { a.hub.CloseUser(user); a.collab.CloseUser(user) }}); err != nil {
		return err
	}
	projects := project.New(a.db, rec, ids, sys.Clock, runner, dir.ReposDir())
	reviews := review.NewService(a.db, rec, ids, sys.Clock, runner, projects, a.hub, notifier)
	files := attachment.New(dir.AttachmentsDir())
	a.inbox = inbox.NewService(a.db, notifier, sys.Clock)
	if err := a.repair(ctx, projects, reviews); err != nil {
		return err
	}
	if err := a.auth.RefreshActive(ctx); err != nil {
		return err
	}

	scheme := "https"
	if cfg.InsecureHTTP {
		scheme = "http"
	}
	mainAddrs := listenAt(cfg.Listen, addrs, cfg.Port)
	for _, ap := range mainAddrs {
		l, err := a.listen(ctx, ap.String())
		if err != nil {
			return fmt.Errorf("app: listen on %s: %w", ap, err)
		}
		a.mainLns = append(a.mainLns, l)
	}
	port := a.mainLns[0].Addr().(*net.TCPAddr).Port
	a.mainURL = scheme + "://" + net.JoinHostPort(cfg.Host, strconv.Itoa(port))
	enrollURL := "https://" + net.JoinHostPort(cfg.Host, strconv.Itoa(int(enrollPort(cfg))))

	enroll := &enrollListeners{
		listen: a.listen,
		addrs:  listenAt(cfg.EnrollListen, addrs, cfg.EnrollPort), allowed: a.allowed, log: sys.Log,
		tls: server.EnrollTLS(a.certs),
	}
	a.gate = newGate(a.auth.OpenInvitationsUntil, enroll, sys.Clock, sys.Log)
	enroll.handler = httpapi.NewEnrollHandler(httpapi.EnrollConfig{Auth: a.auth, CA: a.ca, Log: sys.Log, MainURL: a.mainURL,
		OnEnrolled: a.gate.Changed})
	api := httpapi.New(httpapi.Config{Auth: a.auth, Projects: projects, Reviews: reviews,
		Comments:    comment.New(a.db, rec, ids, sys.Clock, runner, projects, a.hub, notifier),
		Discussions: discussion.New(a.db, rec, ids, sys.Clock, a.hub),
		Attachments: attachment.NewService(a.db, rec, ids, sys.Clock, files, attachment.DefaultMaxSize),
		Inbox:       a.inbox,
		Admin:       admin.New(admin.Config{DB: a.db, Audit: rec, IDs: ids, Clock: sys.Clock, Runner: runner, Projects: projects, Files: files, Collab: a.collab}),
		Hub:         a.hub, Collab: a.collab, Origin: a.mainURL, ClientCerts: cfg.ClientCerts, Active: active,
		EnrollURL: enrollURL, CAFingerprint: pki.Fingerprint(a.ca.Cert), OnInvitationsChanged: a.gate.Changed, Log: sys.Log})
	git := gitserver.New(gitserver.Config{Auth: a.auth, Projects: projects, Reviews: reviews, Runner: runner,
		ClientCerts: cfg.ClientCerts, Active: active, Log: sys.Log, MaxPush: gitserver.DefaultMaxPush})
	a.main = &http.Server{Handler: router(api, git, webui.Handler(webui.Files())), ReadHeaderTimeout: readHeaderTimeout, IdleTimeout: idleTimeout,
		ErrorLog: slog.NewLogLogger(sys.Log.Handler(), slog.LevelWarn)}
	if !cfg.InsecureHTTP {
		a.main.TLSConfig = server.MainTLS(mainCert, a.ca.Cert, active, cfg.ClientCerts, sys.Clock.Now)
	}
	return nil
}

func (a *App) listen(ctx context.Context, address string) (net.Listener, error) {
	if a.sys.Listen != nil {
		return a.sys.Listen(ctx, address)
	}
	return new(net.ListenConfig).Listen(ctx, "tcp", address)
}

// repair runs the startup checks that finish work a crash interrupted.
func (a *App) repair(ctx context.Context, projects *project.Service, reviews *review.Service) error {
	removed, err := projects.RemoveOrphanRepositories(ctx)
	if err != nil {
		return fmt.Errorf("app: remove orphan repositories: %w", err)
	}
	if len(removed) > 0 {
		a.sys.Log.Warn("removed repositories without a project", "names", removed)
	}
	pins, err := reviews.RepairPins(ctx)
	if err != nil {
		return fmt.Errorf("app: repair revision refs: %w", err)
	}
	if pins > 0 {
		a.sys.Log.Warn("re-created missing revision refs", "count", pins)
	}
	return nil
}

func (a *App) banner() {
	w := a.sys.Out
	_, _ = fmt.Fprintf(w, "garret listening on %s\n", a.mainURL)
	for _, l := range a.mainLns {
		_, _ = fmt.Fprintf(w, "  address %s\n", l.Addr())
	}
	for _, p := range a.allowed {
		_, _ = fmt.Fprintf(w, "  allowed network %s\n", p)
	}
	_, _ = fmt.Fprintf(w, "  CA fingerprint (SHA-256) %s\n", pki.Fingerprint(a.ca.Cert))
	if !a.cfg.ClientCerts {
		_, _ = fmt.Fprintln(w, "warning: client certificates are off; any device on the allowed networks can reach the login page")
	}
	if !webui.Built() {
		_, _ = fmt.Fprintln(w, "note: web interface: not built (this binary serves a placeholder page; build with make build)")
	}
	if a.cfg.InsecureHTTP {
		_, _ = fmt.Fprintln(w, "warning: plain HTTP is on; passwords and code cross the network unencrypted")
	}
}

// diskWarning prints whether disk encryption appears to be off. It never
// stops startup.
func (a *App) diskWarning(ctx context.Context) {
	if a.sys.DiskProbe == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, diskProbeLimit)
	defer cancel()
	switch diskcrypt.Check(ctx, *a.sys.DiskProbe) {
	case diskcrypt.Off:
		_, _ = fmt.Fprintln(a.sys.Out, "warning: disk encryption appears to be off; garret does not encrypt its data directory")
	case diskcrypt.Unknown:
		_, _ = fmt.Fprintln(a.sys.Out, "note: could not tell whether disk encryption is on")
	}
}

// MainURL returns the main service's URL.
func (a *App) MainURL() string { return a.mainURL }

// CA returns the CA certificate.
func (a *App) CA() *x509.Certificate { return a.ca.Cert }

// Fingerprint returns the CA's SHA-256 fingerprint.
func (a *App) Fingerprint() string { return pki.Fingerprint(a.ca.Cert) }

// Run serves until ctx ends, then shuts down in the order of transport.md
// 2.4. It returns the first serving error, or nil after a clean shutdown.
func (a *App) Run(ctx context.Context) error {
	bg, stopBG := context.WithCancel(context.WithoutCancel(ctx))
	defer stopBG()
	var wg sync.WaitGroup
	wg.Go(func() { a.hub.Run(bg) })
	wg.Go(func() { a.collab.Run(bg) })
	wg.Go(func() { a.certs.Run(bg, certificateCheck) })
	refresh := activeRefresh
	if a.sys.ActiveRefresh > 0 {
		refresh = a.sys.ActiveRefresh
	}
	wg.Go(func() { every(bg, refresh, a.refreshActive) })
	wg.Go(func() { every(bg, sessionSweep, a.sweepSessions) })
	wg.Go(func() { every(bg, notificationSweep, a.sweepNotifications) })
	gateCtx, stopGate := context.WithCancel(bg)
	gateDone := make(chan struct{})
	go func() { a.gate.Run(gateCtx); close(gateDone) }()

	serveErr := make(chan error, len(a.mainLns))
	for _, l := range a.mainLns {
		l := netguard.Listener(l, a.allowed, a.sys.Log)
		go func() { serveErr <- a.serve(l) }()
	}
	var err error
	select {
	case <-ctx.Done():
	case err = <-serveErr:
	}
	grace, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	if shutErr := a.main.Shutdown(grace); shutErr != nil {
		a.sys.Log.Warn("shutdown: requests still running after the grace period", "err", shutErr)
		_ = a.main.Close()
	}
	cancel()
	stopGate()
	<-gateDone
	stopBG()
	wg.Wait()
	if closeErr := a.db.Close(); err == nil {
		err = closeErr
	}
	return err
}

func (a *App) serve(l net.Listener) error {
	var err error
	if a.cfg.InsecureHTTP {
		err = a.main.Serve(l)
	} else {
		err = a.main.ServeTLS(l, "", "")
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (a *App) refreshActive(ctx context.Context) {
	if err := a.auth.RefreshActive(ctx); err != nil {
		a.sys.Log.Warn("refreshing active certificates failed", "err", err)
	}
}

func (a *App) sweepSessions(ctx context.Context) {
	if _, err := a.auth.SweepSessions(ctx); err != nil {
		a.sys.Log.Warn("session sweep failed", "err", err)
	}
}

func (a *App) sweepNotifications(ctx context.Context) {
	if _, err := a.inbox.Sweep(ctx); err != nil {
		a.sys.Log.Warn("notification sweep failed", "err", err)
	}
}

// closeAll releases what New opened when New fails.
func (a *App) closeAll() {
	for _, l := range a.mainLns {
		_ = l.Close()
	}
	if a.db != nil {
		_ = a.db.Close()
	}
}

// caAddresses are the listen addresses the server certificate names,
// without loopback, which the CA and certificates always cover.
func caAddresses(addrs []netip.Addr, cfg config.Serve) []netip.Addr {
	var out []netip.Addr
	for _, a := range addrs {
		if !a.IsLoopback() {
			out = append(out, a)
		}
	}
	if cfg.Listen.IsValid() && !cfg.Listen.Addr().IsLoopback() && !cfg.Listen.Addr().IsUnspecified() {
		out = append(out, cfg.Listen.Addr())
	}
	return out
}

// listenAt is the override when given, else every address on port.
func listenAt(override netip.AddrPort, addrs []netip.Addr, port uint16) []netip.AddrPort {
	if override.IsValid() {
		return []netip.AddrPort{override}
	}
	out := make([]netip.AddrPort, len(addrs))
	for i, a := range addrs {
		out[i] = netip.AddrPortFrom(a, port)
	}
	return out
}

func enrollPort(cfg config.Serve) uint16 {
	if cfg.EnrollListen.IsValid() {
		return cfg.EnrollListen.Port()
	}
	return cfg.EnrollPort
}

// enrollListeners are the enrollment listeners the gate opens and closes.
// Only the gate's goroutine calls Open and Close.
type enrollListeners struct {
	listen  func(ctx context.Context, address string) (net.Listener, error)
	addrs   []netip.AddrPort
	allowed []netip.Prefix
	tls     *tls.Config
	handler http.Handler
	log     *slog.Logger
	srv     *http.Server
	wg      sync.WaitGroup
}

// Open listens on every enrollment address and serves the enrollment
// handler. If one address fails, the ones already open are closed.
func (e *enrollListeners) Open() error {
	srv := &http.Server{Handler: e.handler, TLSConfig: e.tls, ReadHeaderTimeout: readHeaderTimeout, IdleTimeout: idleTimeout,
		ErrorLog: slog.NewLogLogger(e.log.Handler(), slog.LevelWarn)}
	var lns []net.Listener
	for _, ap := range e.addrs {
		l, err := e.listen(context.Background(), ap.String())
		if err != nil {
			for _, o := range lns {
				_ = o.Close()
			}
			return fmt.Errorf("app: enrollment listen on %s: %w", ap, err)
		}
		lns = append(lns, netguard.Listener(l, e.allowed, e.log))
	}
	for _, l := range lns {
		e.wg.Go(func() {
			if err := srv.ServeTLS(l, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				e.log.Error("enrollment listener stopped", "err", err)
			}
		})
	}
	e.srv = srv
	return nil
}

// Close stops the enrollment server, giving requests a short grace.
func (e *enrollListeners) Close() error {
	if e.srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), enrollStopGrace)
	defer cancel()
	err := e.srv.Shutdown(ctx)
	if err != nil {
		_ = e.srv.Close()
	}
	e.wg.Wait()
	e.srv = nil
	return err
}
