package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

// parseFlags parses args with fs for the command whose usage line is
// synopsis. With -h or --help it prints the usage line and the flags to
// stdout and returns exitOK; with a bad flag it prints the error, the usage
// line and the flags to stderr and returns exitUsage. done is false only when
// parsing succeeded and the command should run.
func parseFlags(fs *flag.FlagSet, synopsis string, args []string, stdout, stderr io.Writer) (code int, done bool) {
	fs.SetOutput(stderr)
	fs.Usage = func() {} // printed below, to the stream that fits
	err := fs.Parse(args)
	if err == nil {
		return exitOK, false
	}
	w, code := stderr, exitUsage
	if errors.Is(err, flag.ErrHelp) {
		w, code = stdout, exitOK
	}
	_, _ = fmt.Fprintf(w, "usage: %s\n", synopsis)
	fs.SetOutput(w)
	fs.PrintDefaults()
	return code, true
}
