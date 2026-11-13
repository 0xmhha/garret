package main

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/0xmhha/garret/internal/config"
)

// joinMode is how members join a server (decisions.md D14).
type joinMode string

const (
	// joinEnroll: each member runs garret enroll; client certificates on.
	joinEnroll joinMode = "enroll"
	// joinBrowser: a browser and a password only; client certificates off.
	joinBrowser joinMode = "browser"
)

// joinModeFile keeps the mode in the data directory.
const joinModeFile = "join-mode"

// joinModeTries is how many unreadable answers the first start accepts.
const joinModeTries = 3

var errJoinModeAnswer = errors.New("answer 1 or 2")

// fileStore is the part of the data directory the mode lives in.
type fileStore interface {
	ReadFile(rel string) ([]byte, error)
	WriteFile(rel string, data []byte) error
}

// resolveClientCerts decides whether this run requires client
// certificates: a --client-cert or --insecure-http flag wins for this run;
// otherwise the kept mode; otherwise, on the first start, the answer of ask
// (nil when there is no terminal, which means enroll), which is then kept.
func resolveClientCerts(cfg config.Serve, dir fileStore, ask func() (joinMode, error)) (bool, error) {
	if cfg.ClientCertsSet {
		return cfg.ClientCerts, nil
	}
	b, err := dir.ReadFile(joinModeFile)
	switch {
	case err == nil:
		switch joinMode(strings.TrimSpace(string(b))) {
		case joinEnroll:
			return true, nil
		case joinBrowser:
			return false, nil
		}
		return false, fmt.Errorf("%s in the data directory holds %q; delete it to choose again", joinModeFile, strings.TrimSpace(string(b)))
	case !errors.Is(err, fs.ErrNotExist):
		return false, err
	}
	mode := joinEnroll
	if ask != nil {
		if mode, err = ask(); err != nil {
			return false, err
		}
	}
	if err := dir.WriteFile(joinModeFile, []byte(string(mode)+"\n")); err != nil {
		return false, fmt.Errorf("keep the join mode: %w", err)
	}
	return mode == joinEnroll, nil
}

// askJoinMode asks how members will join, with enroll as the default.
func askJoinMode(p Prompter) (joinMode, error) {
	const label = "How will members join this server?\n" +
		"  1  Each member runs garret enroll once (client certificates; recommended)\n" +
		"  2  A browser and a password only (client certificates off: anyone on the\n" +
		"     allowed network can reach the sign-in page)\n" +
		"Choose 1 or 2"
	for range joinModeTries {
		a, err := p.Ask(label, "1")
		if err != nil {
			return "", err
		}
		switch strings.TrimSpace(a) {
		case "1":
			return joinEnroll, nil
		case "2":
			return joinBrowser, nil
		}
	}
	return "", errJoinModeAnswer
}
