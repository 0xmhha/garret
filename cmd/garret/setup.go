package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strconv"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/config"
	"github.com/0xmhha/garret/internal/datadir"
	"github.com/0xmhha/garret/internal/enroll"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/pki"
	"github.com/0xmhha/garret/internal/store"
)

var errAdminExists = errors.New("an administrator already exists; ask them for an invitation")

// adminSetup creates the first administrator from the data directory and
// installs its certificate on this machine (transport.md 7.1).
func adminSetup(args []string, d deps) int {
	env := config.Environment{OS: d.platform.GOOS, Home: d.platform.Home, XDGDataHome: os.Getenv("XDG_DATA_HOME"), Hostname: d.hostname}
	fset := flag.NewFlagSet("admin setup", flag.ContinueOnError)
	fset.SetOutput(d.stderr)
	data := fset.String("data", config.DefaultDataDir(env), "data directory of the server")
	host := fset.String("host", config.DefaultHost(d.hostname), "host name the server uses")
	port := fset.Uint("port", config.DefaultPort, "main port of the server")
	if err := fset.Parse(args); err != nil || fset.NArg() > 0 || *port == 0 || *port > 65535 {
		return exitUsage
	}
	if _, err := os.Stat(*data); errors.Is(err, fs.ErrNotExist) {
		return fail(d, "admin setup", pki.ErrNoCA)
	}
	ctx := context.Background()
	dir, err := datadir.Open(*data)
	if err != nil {
		return fail(d, "admin setup", err)
	}
	ca, err := pki.LoadCA(dir)
	if err != nil {
		return fail(d, "admin setup", err)
	}
	db, err := store.Open(ctx, dir.DBPath())
	if err != nil {
		return fail(d, "admin setup", err)
	}
	defer func() { _ = db.Close() }()
	ids, err := id.NewGenerator(d.clock, rand.Reader)
	if err != nil {
		return fail(d, "admin setup", err)
	}
	svc, err := auth.NewService(auth.Config{DB: db, Audit: audit.NewRecorder(ids, d.clock), IDs: ids, Clock: d.clock, Rand: rand.Reader,
		CA: ca, Active: &pki.ActiveSerials{}, Password: auth.Default})
	if err != nil {
		return fail(d, "admin setup", err)
	}
	token, err := svc.SetupToken()
	if err != nil {
		return fail(d, "admin setup", err)
	}
	if token == "" {
		return fail(d, "admin setup", errAdminExists)
	}
	acct, err := account(d.prompt, d.hostname)
	if err != nil {
		return fail(d, "admin setup", err)
	}
	key, err := enroll.NewKey()
	if err != nil {
		return fail(d, "admin setup", err)
	}
	csr, err := enroll.CSR(key)
	if err != nil {
		return fail(d, "admin setup", err)
	}
	issued, err := svc.SetupAdmin(ctx, token, acct, csr)
	if errors.Is(err, auth.ErrSetupClosed) {
		return fail(d, "admin setup", errAdminExists)
	}
	if err != nil {
		return fail(d, "admin setup", err)
	}
	_, _ = fmt.Fprintf(d.stdout, "Created the administrator %s.\n", acct.Username)
	serverURL := "https://" + net.JoinHostPort(*host, strconv.FormatUint(uint64(*port), 10))
	return install(ctx, d, "admin setup", serverURL, key, enroll.Result{Certificate: issued.Certificate, CA: ca.Cert, ServerURL: serverURL})
}
