package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestRunDispatch(t *testing.T) {
	tests := []struct {
		args     []string
		code     int
		inStdout string
		inStderr string
	}{
		{[]string{"version"}, exitOK, "garret ", ""},
		{nil, exitUsage, "", "usage: garret"},
		{[]string{"frobnicate"}, exitUsage, "", `unknown command "frobnicate"`},
		{[]string{"help"}, exitOK, "usage: garret", ""},
		{[]string{"serve", "--client-cert=maybe"}, exitUsage, "", "--client-cert must be on or off"},
		{[]string{"serve", "extra"}, exitUsage, "", "unexpected argument"},
	}
	for _, tt := range tests {
		var out, errOut bytes.Buffer
		code := run(tt.args, &out, &errOut)
		if code != tt.code || !strings.Contains(out.String(), tt.inStdout) || !strings.Contains(errOut.String(), tt.inStderr) {
			t.Errorf("run(%q) = %d\nstdout %q\nstderr %q", tt.args, code, out.String(), errOut.String())
		}
	}
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }
