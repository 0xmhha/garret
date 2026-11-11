package review

import (
	"errors"

	"github.com/0xmhha/garret/internal/gitrepo"
)

// ErrRange is returned by Port for an invalid line range or for blocks that
// are not in order.
var ErrRange = errors.New("review: invalid range or blocks")

// Block is a run of consecutive changed lines. A pure insertion has
// OldLines 0 and OldStart set to the old line after which the lines were
// inserted (0 for the top of the file); a pure deletion has NewLines 0 and
// NewStart set the same way on the new side.
type Block struct {
	OldStart, OldLines int
	NewStart, NewLines int
}

// Position is where a ported comment goes on the new revision. FileLevel
// means the lines it was written on were changed, so it is shown on the file
// instead of on lines.
type Position struct {
	Start, End int
	FileLevel  bool
}

// ChangeBlocks reduces hunks to blocks of changed lines, dropping context, so
// porting does not depend on how much context the diff was asked for.
func ChangeBlocks(hunks []gitrepo.Hunk) []Block {
	var blocks []Block
	for _, h := range hunks {
		// The next line on each side; a side with no lines starts after Start.
		nextOld, nextNew := h.OldStart, h.NewStart
		if h.OldLines == 0 {
			nextOld++
		}
		if h.NewLines == 0 {
			nextNew++
		}
		var cur *Block
		for _, l := range h.Lines {
			if l.Kind == gitrepo.LineContext {
				cur = closeBlock(&blocks, cur)
				nextOld++
				nextNew++
				continue
			}
			if cur == nil {
				cur = &Block{OldStart: nextOld, NewStart: nextNew}
			}
			if l.Kind == gitrepo.LineDeleted {
				cur.OldLines++
				nextOld++
			} else {
				cur.NewLines++
				nextNew++
			}
		}
		closeBlock(&blocks, cur)
	}
	return blocks
}

// closeBlock appends b, adjusting the start of an empty side to "after the
// previous line", and returns nil.
func closeBlock(blocks *[]Block, b *Block) *Block {
	if b == nil {
		return nil
	}
	if b.OldLines == 0 {
		b.OldStart--
	}
	if b.NewLines == 0 {
		b.NewStart--
	}
	*blocks = append(*blocks, *b)
	return nil
}

// Port maps the old-side line range start..end (1-based, inclusive) to the
// new side through blocks, which must be in order and not overlap. If the
// range touches a changed line or an insertion splits it, the result is
// FileLevel. It returns ErrRange for start < 1, end < start, or bad blocks.
func Port(blocks []Block, start, end int) (Position, error) {
	if start < 1 || end < start || !ordered(blocks) {
		return Position{}, ErrRange
	}
	shift := 0
	for _, b := range blocks {
		if b.OldLines == 0 {
			// Inserted after old line b.OldStart.
			switch {
			case end <= b.OldStart:
				return Position{Start: start + shift, End: end + shift}, nil
			case start > b.OldStart:
				shift += b.NewLines
				continue
			}
			return Position{FileLevel: true}, nil
		}
		last := b.OldStart + b.OldLines - 1
		switch {
		case end < b.OldStart:
			return Position{Start: start + shift, End: end + shift}, nil
		case start > last:
			shift += b.NewLines - b.OldLines
			continue
		}
		return Position{FileLevel: true}, nil
	}
	return Position{Start: start + shift, End: end + shift}, nil
}

// ordered reports whether blocks have no negative fields, are sorted by old
// position and do not overlap. An insertion may sit right after a deletion.
func ordered(blocks []Block) bool {
	prevEnd := -1 // last old line covered by the previous block
	for _, b := range blocks {
		if b.OldStart < 0 || b.OldLines < 0 || b.NewStart < 0 || b.NewLines < 0 {
			return false
		}
		if b.OldLines == 0 {
			if b.OldStart < prevEnd {
				return false
			}
			prevEnd = b.OldStart
			continue
		}
		if b.OldStart <= prevEnd {
			return false
		}
		prevEnd = b.OldStart + b.OldLines - 1
	}
	return true
}
