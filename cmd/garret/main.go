package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"

	"github.com/0xmhha/garret/internal/app"
	"github.com/0xmhha/garret/internal/clock"
	"github.com/0xmhha/garret/internal/config"
	"github.com/0xmhha/garret/internal/diskcrypt"
	"github.com/0xmhha/garret/internal/netguard"
	"github.com/0xmhha/garret/internal/webui"
)

// Exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usage = `usage: garret <command> [flags]

commands:
  serve          start the server
  admin setup    create the first administrator, on the server's machine
  enroll <link>  join a server with an invitation link
  renew          renew this device's certificates
  version        print the version and Git commit
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches a command with the real machine and returns the process
// exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "serve" {
		return serve(args[1:], stdout, stderr)
	}
	d, err := realDeps(stdout, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "garret: %v\n", err)
		return exitError
	}
	return runWith(args, d)
}

// runWith dispatches a command other than serve with the given machine.
func runWith(args []string, d deps) int {
	stdout, stderr := d.stdout, d.stderr
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "enroll":
		return enrollCmd(args[1:], d)
	case "renew":
		return renewCmd(args[1:], d)
	case "admin":
		if len(args) > 1 && args[1] == "setup" {
			return adminSetup(args[2:], d)
		}
		_, _ = fmt.Fprint(stderr, usage)
		return exitUsage
	case "version":
		_, _ = fmt.Fprintln(stdout, version())
		return exitOK
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, usage)
		return exitOK
	default:
		_, _ = fmt.Fprintf(stderr, "garret: unknown command %q\n\n%s", args[0], usage)
		return exitUsage
	}
}

// version reports the module version and the VCS revision the binary was
// built from.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "garret (unknown build)"
	}
	rev, dirty := "unknown", ""
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "+dirty"
			}
		}
	}
	web := "built"
	if !webui.Built() {
		web = "placeholder"
	}
	return fmt.Sprintf("garret %s (commit %s%s, %s, web: %s)", info.Main.Version, rev, dirty, runtime.Version(), web)
}

func serve(args []string, stdout, stderr io.Writer) int {
	env, err := environment()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "garret: %v\n", err)
		return exitError
	}
	cfg, err := config.ParseServe(args, env)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "garret serve: %v\n", err)
		return exitUsage
	}
	git, err := exec.LookPath("git")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "garret serve: git is not installed or not on PATH")
		return exitError
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	probe := &diskcrypt.Probe{GOOS: runtime.GOOS, Root: os.DirFS("/"), FileVault: fileVaultStatus}
	a, err := app.New(ctx, cfg, app.System{Clock: clock.System{}, Rand: rand.Reader, Interfaces: netguard.Interfaces,
		GitPath: git, Log: log, Out: stdout, DiskProbe: probe})
	if err != nil {
		log.Error("startup failed", "err", err)
		return exitError
	}
	if err := a.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("server stopped", "err", err)
		return exitError
	}
	return exitOK
}

// fileVaultStatus runs `fdesetup status`, which reports FileVault without
// needing administrator rights.
func fileVaultStatus(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "/usr/bin/fdesetup", "status").Output()
	return string(out), err
}

func environment() (config.Environment, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config.Environment{}, err
	}
	host, _ := os.Hostname()
	return config.Environment{OS: runtime.GOOS, Home: home, XDGDataHome: os.Getenv("XDG_DATA_HOME"), Hostname: host}, nil
}
