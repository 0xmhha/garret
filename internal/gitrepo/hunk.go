package gitrepo

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

var (
	// ErrPatchFormat is returned when a patch does not have the unified diff
	// form git produces.
	ErrPatchFormat = errors.New("gitrepo: unexpected patch format")
	// ErrTooLarge is returned when a patch exceeds the caller's limits, so
	// the file is reported as too large instead of shown.
	ErrTooLarge = errors.New("gitrepo: patch too large")
)

// LineKind says which side of a diff a line belongs to.
type LineKind byte

// Line kinds, by their marker in the patch.
const (
	LineContext LineKind = ' '
	LineAdded   LineKind = '+'
	LineDeleted LineKind = '-'
)

const noNewlineMarker = '\\'

// Limits bound the size of one file's patch.
type Limits struct {
	MaxBytes int
	MaxLines int
}

// Hunk is one @@ block of a patch.
type Hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
	Section            string // text after the closing @@, for display only
	Lines              []Line
}

// Line is one line of a hunk. OldNo and NewNo are 1-based line numbers, 0 on
// the side where the line does not exist.
type Line struct {
	Kind           LineKind
	Text           []byte // without the marker and the line ending newline
	OldNo, NewNo   int
	NoNewlineAtEOF bool
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(?: (.*))?$`)

// maxLineNumber keeps every number parsed from a header within int32, so
// sums of starts and counts cannot overflow.
const maxLineNumber = 1<<31 - 1

// ParseHunks parses the patch of one file produced by
// git diff --no-color --no-ext-diff. Lines before the first hunk header are
// skipped. It returns ErrTooLarge when the patch exceeds limits and an error
// wrapping ErrPatchFormat, with the line number, when the patch is not
// well formed. Text slices alias patch.
func ParseHunks(patch []byte, limits Limits) ([]Hunk, error) {
	if len(patch) > limits.MaxBytes {
		return nil, ErrTooLarge
	}
	lines := bytes.Split(bytes.TrimSuffix(patch, []byte{'\n'}), []byte{'\n'})
	if len(patch) == 0 {
		lines = nil
	}
	p := hunkParser{limits: limits}
	for i, l := range lines {
		if err := p.line(l); err != nil {
			if errors.Is(err, ErrTooLarge) {
				return nil, err
			}
			return nil, fmt.Errorf("%w on line %d", err, i+1)
		}
	}
	if p.cur != nil && (p.oldLeft > 0 || p.newLeft > 0) {
		return nil, fmt.Errorf("%w: hunk ends early", ErrPatchFormat)
	}
	return p.hunks, nil
}

type hunkParser struct {
	limits           Limits
	hunks            []Hunk
	cur              *Hunk
	oldNo, newNo     int
	oldLeft, newLeft int
	lineCount        int
}

func (p *hunkParser) line(l []byte) error {
	if p.cur == nil || (p.oldLeft == 0 && p.newLeft == 0) {
		if bytes.HasPrefix(l, []byte("@@ ")) {
			return p.header(l)
		}
		if p.cur == nil {
			return nil // file headers before the first hunk
		}
		if len(l) > 0 && l[0] == noNewlineMarker {
			return p.markNoNewline()
		}
		return ErrPatchFormat
	}
	if len(l) == 0 {
		return ErrPatchFormat
	}
	if l[0] == noNewlineMarker {
		return p.markNoNewline()
	}
	p.lineCount++
	if p.lineCount > p.limits.MaxLines {
		return ErrTooLarge
	}
	line := Line{Kind: LineKind(l[0]), Text: l[1:]}
	switch line.Kind {
	case LineContext:
		if p.oldLeft == 0 || p.newLeft == 0 {
			return ErrPatchFormat
		}
		line.OldNo, line.NewNo = p.oldNo, p.newNo
		p.oldNo, p.newNo, p.oldLeft, p.newLeft = p.oldNo+1, p.newNo+1, p.oldLeft-1, p.newLeft-1
	case LineDeleted:
		if p.oldLeft == 0 {
			return ErrPatchFormat
		}
		line.OldNo = p.oldNo
		p.oldNo, p.oldLeft = p.oldNo+1, p.oldLeft-1
	case LineAdded:
		if p.newLeft == 0 {
			return ErrPatchFormat
		}
		line.NewNo = p.newNo
		p.newNo, p.newLeft = p.newNo+1, p.newLeft-1
	default:
		return ErrPatchFormat
	}
	p.cur.Lines = append(p.cur.Lines, line)
	return nil
}

func (p *hunkParser) markNoNewline() error {
	if p.cur == nil || len(p.cur.Lines) == 0 {
		return ErrPatchFormat
	}
	p.cur.Lines[len(p.cur.Lines)-1].NoNewlineAtEOF = true
	return nil
}

func (p *hunkParser) header(l []byte) error {
	m := hunkHeader.FindSubmatch(l)
	if m == nil {
		return ErrPatchFormat
	}
	var n [4]int
	for i, s := range m[1:5] {
		n[i] = 1 // an omitted count means one line
		if s == nil {
			continue
		}
		v, err := strconv.ParseUint(string(s), 10, 31)
		if err != nil || v > maxLineNumber {
			return ErrPatchFormat
		}
		n[i] = int(v)
	}
	h := Hunk{OldStart: n[0], OldLines: n[1], NewStart: n[2], NewLines: n[3], Section: string(m[5])}
	// A start of 0 is used only by a side with no lines (new or deleted file).
	if (h.OldStart == 0 && h.OldLines != 0) || (h.NewStart == 0 && h.NewLines != 0) {
		return ErrPatchFormat
	}
	if len(p.hunks) > 0 {
		prev := p.hunks[len(p.hunks)-1]
		if h.OldStart < prev.OldStart+prev.OldLines || h.NewStart < prev.NewStart+prev.NewLines {
			return ErrPatchFormat
		}
	}
	p.hunks = append(p.hunks, h)
	p.cur = &p.hunks[len(p.hunks)-1]
	p.oldNo, p.newNo = h.OldStart, h.NewStart
	p.oldLeft, p.newLeft = h.OldLines, h.NewLines
	return nil
}
