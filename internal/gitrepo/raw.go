package gitrepo

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
)

// ErrRawFormat is returned when git diff --raw -z or --numstat -z output
// does not have the expected form.
var ErrRawFormat = errors.New("gitrepo: unexpected diff summary format")

// Status is the kind of change git reports for one file.
type Status byte

// Statuses that git diff --raw -M reports.
const (
	StatusAdded       Status = 'A'
	StatusModified    Status = 'M'
	StatusDeleted     Status = 'D'
	StatusRenamed     Status = 'R'
	StatusTypeChanged Status = 'T'
)

// FileChange is one record of git diff --raw -z -M. OldPath is empty for an
// added file and NewPath for a deleted one. Paths are kept byte for byte.
type FileChange struct {
	Status           Status
	OldPath, NewPath string
	OldMode, NewMode string
	OldSHA, NewSHA   string
	Similarity       int // 0-100, set for renames only
}

const (
	modeLen        = 6
	minSHALen      = 4
	maxSHALen      = 64
	rawMetaFields  = 5
	maxSimilarity  = 100
	similarityLen  = 3
	nulByte        = 0
	rawRecordStart = ':'
)

// ParseRaw parses the output of git diff --raw -z -M. Records keep git's
// order. It returns an error wrapping ErrRawFormat, with the byte offset,
// for anything else, including copies and unmerged entries.
func ParseRaw(out []byte) ([]FileChange, error) {
	var changes []FileChange
	r := &nulReader{buf: out}
	for !r.done() {
		start := r.off
		meta, ok := r.next()
		if !ok || len(meta) == 0 || meta[0] != rawRecordStart {
			return nil, rawError(start)
		}
		c, paths, ok := parseRawMeta(meta[1:])
		if !ok {
			return nil, rawError(start)
		}
		names := make([]string, paths)
		for i := range names {
			p, ok := r.next()
			if !ok || len(p) == 0 {
				return nil, rawError(r.off)
			}
			names[i] = string(p)
		}
		switch c.Status {
		case StatusAdded:
			c.NewPath = names[0]
		case StatusDeleted:
			c.OldPath = names[0]
		case StatusRenamed:
			c.OldPath, c.NewPath = names[0], names[1]
		default:
			c.OldPath, c.NewPath = names[0], names[0]
		}
		changes = append(changes, c)
	}
	return changes, nil
}

func parseRawMeta(meta []byte) (FileChange, int, bool) {
	f := bytes.Split(meta, []byte{' '})
	if len(f) != rawMetaFields || !isMode(f[0]) || !isMode(f[1]) || !isHex(f[2]) || !isHex(f[3]) {
		return FileChange{}, 0, false
	}
	c := FileChange{OldMode: string(f[0]), NewMode: string(f[1]), OldSHA: string(f[2]), NewSHA: string(f[3])}
	status := f[4]
	if len(status) == 0 {
		return FileChange{}, 0, false
	}
	c.Status = Status(status[0])
	switch c.Status {
	case StatusAdded, StatusModified, StatusDeleted, StatusTypeChanged:
		return c, 1, len(status) == 1
	case StatusRenamed:
		if len(status) != 1+similarityLen || !isDigits(status[1:]) {
			return FileChange{}, 0, false
		}
		n, err := strconv.Atoi(string(status[1:]))
		if err != nil || n > maxSimilarity {
			return FileChange{}, 0, false
		}
		c.Similarity = n
		return c, 2, true
	}
	return FileChange{}, 0, false
}

// LineCount is one record of git diff --numstat -z -M.
type LineCount struct {
	Added, Deleted   int
	Binary           bool
	OldPath, NewPath string
}

// ParseNumstat parses the output of git diff --numstat -z -M. A binary file,
// which git reports as "-\t-", has Binary set and zero counts.
func ParseNumstat(out []byte) ([]LineCount, error) {
	var counts []LineCount
	r := &nulReader{buf: out}
	for !r.done() {
		start := r.off
		field, ok := r.next()
		if !ok {
			return nil, rawError(start)
		}
		f := bytes.SplitN(field, []byte{'\t'}, 3)
		if len(f) != 3 {
			return nil, rawError(start)
		}
		var lc LineCount
		if lc.Added, lc.Deleted, lc.Binary, ok = parseCounts(f[0], f[1]); !ok {
			return nil, rawError(start)
		}
		if len(f[2]) > 0 {
			lc.OldPath, lc.NewPath = string(f[2]), string(f[2])
		} else {
			// A rename leaves the path empty and follows with two paths.
			oldPath, ok1 := r.next()
			newPath, ok2 := r.next()
			if !ok1 || !ok2 || len(oldPath) == 0 || len(newPath) == 0 {
				return nil, rawError(start)
			}
			lc.OldPath, lc.NewPath = string(oldPath), string(newPath)
		}
		counts = append(counts, lc)
	}
	return counts, nil
}

func parseCounts(a, d []byte) (added, deleted int, binary, ok bool) {
	if string(a) == "-" && string(d) == "-" {
		return 0, 0, true, true
	}
	if !isDigits(a) || !isDigits(d) {
		return 0, 0, false, false
	}
	x, err1 := strconv.Atoi(string(a))
	y, err2 := strconv.Atoi(string(d))
	return x, y, false, err1 == nil && err2 == nil
}

// nulReader splits a buffer into NUL-terminated fields.
type nulReader struct {
	buf []byte
	off int
}

func (r *nulReader) done() bool { return r.off >= len(r.buf) }

// next returns the field up to the next NUL and advances past it. It reports
// false if no NUL terminates the field.
func (r *nulReader) next() ([]byte, bool) {
	i := bytes.IndexByte(r.buf[r.off:], nulByte)
	if i < 0 {
		return nil, false
	}
	field := r.buf[r.off : r.off+i]
	r.off += i + 1
	return field, true
}

func rawError(off int) error { return fmt.Errorf("%w at byte %d", ErrRawFormat, off) }

func isMode(b []byte) bool {
	if len(b) != modeLen {
		return false
	}
	for _, c := range b {
		if c < '0' || c > '7' {
			return false
		}
	}
	return true
}

func isHex(b []byte) bool {
	if len(b) < minSHALen || len(b) > maxSHALen {
		return false
	}
	for _, c := range b {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func isDigits(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
