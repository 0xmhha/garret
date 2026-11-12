package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// Prompter asks the person at the terminal. Secrets are read without echo.
type Prompter interface {
	Ask(label, def string) (string, error)
	Secret(label string) (string, error)
}

// linePrompter reads answers from in. When in is a terminal, Secret turns
// echo off; otherwise every prompt reads one line, so answers can be piped.
type linePrompter struct {
	in  io.Reader
	r   *bufio.Reader
	out io.Writer
}

func newPrompter(in io.Reader, out io.Writer) *linePrompter {
	return &linePrompter{in: in, r: bufio.NewReader(in), out: out}
}

// Ask prints label (with def in brackets when set) and returns the answer,
// or def for an empty answer.
func (p *linePrompter) Ask(label, def string) (string, error) {
	if def != "" {
		_, _ = fmt.Fprintf(p.out, "%s [%s]: ", label, def)
	} else {
		_, _ = fmt.Fprintf(p.out, "%s: ", label)
	}
	line, err := p.line()
	if err != nil {
		return "", err
	}
	if line == "" {
		return def, nil
	}
	return line, nil
}

// Secret prints label and reads an answer without echo on a terminal.
func (p *linePrompter) Secret(label string) (string, error) {
	_, _ = fmt.Fprintf(p.out, "%s: ", label)
	if f, ok := p.in.(*os.File); ok && term.IsTerminal(int(f.Fd())) { // #nosec G115 -- a file descriptor fits in int
		b, err := term.ReadPassword(int(f.Fd())) // #nosec G115 -- as above
		_, _ = fmt.Fprintln(p.out)
		return string(b), err
	}
	return p.line()
}

func (p *linePrompter) line() (string, error) {
	s, err := p.r.ReadString('\n')
	if err != nil && (s == "" || !errors.Is(err, io.EOF)) {
		if errors.Is(err, io.EOF) {
			return "", io.ErrUnexpectedEOF
		}
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}
