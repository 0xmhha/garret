package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/0xmhha/garret/internal/webui"
)

func TestRunDispatch(t *testing.T) {
	tests := []struct {
		args     []string
		code     int
		inStdout string
		inStderr string
	}{
		{[]string{"version"}, exitOK, "web: " + map[bool]string{true: "built", false: "placeholder"}[webui.Built()], ""},
		{nil, exitUsage, "", "usage: garret"},
		{[]string{"frobnicate"}, exitUsage, "", `unknown command "frobnicate"`},
		{[]string{"help"}, exitOK, "usage: garret", ""},
		{[]string{"serve", "--client-cert=maybe"}, exitUsage, "", "--client-cert must be on or off"},
		{[]string{"serve", "extra"}, exitUsage, "", "unexpected argument"},
		{[]string{"serve", "--help"}, exitOK, "usage: garret serve [flags]\n", ""},
		{[]string{"serve", "-h"}, exitOK, "-allow value", ""},
		{[]string{"serve", "-h"}, exitOK, "(default: the mode chosen at the first start)\n", ""},
		{[]string{"serve", "--frobnicate"}, exitUsage, "", "usage: garret serve [flags]"},
		{[]string{"enroll", "--help"}, exitOK, "usage: garret enroll [--dry-run] <invitation link>\n", ""},
		{[]string{"enroll", "--frobnicate"}, exitUsage, "", "flag provided but not defined: -frobnicate"},
		{[]string{"renew", "-h"}, exitOK, "-server string", ""},
		{[]string{"admin", "setup", "--help"}, exitOK, "usage: garret admin setup [flags]\n", ""},
	}
	for _, tt := range tests {
		var out, errOut bytes.Buffer
		code := run(tt.args, &out, &errOut)
		if code == exitOK && errOut.Len() > 0 {
			t.Errorf("run(%q) succeeded but wrote to stderr: %q", tt.args, errOut.String())
		}
		if code != tt.code || !strings.Contains(out.String(), tt.inStdout) || !strings.Contains(errOut.String(), tt.inStderr) {
			t.Errorf("run(%q) = %d\nstdout %q\nstderr %q", tt.args, code, out.String(), errOut.String())
		}
	}
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }
