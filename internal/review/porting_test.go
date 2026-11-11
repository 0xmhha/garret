package review

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/0xmhha/garret/internal/gitrepo"
)

func TestChangeBlocks(t *testing.T) {
	l := func(k gitrepo.LineKind, o, n int) gitrepo.Line { return gitrepo.Line{Kind: k, OldNo: o, NewNo: n} }
	const ctx, add, del = gitrepo.LineContext, gitrepo.LineAdded, gitrepo.LineDeleted
	tests := []struct {
		name  string
		hunks []gitrepo.Hunk
		want  []Block
	}{
		{"none", nil, nil},
		{
			"modification inside context",
			[]gitrepo.Hunk{{OldStart: 1, OldLines: 3, NewStart: 1, NewLines: 3, Lines: []gitrepo.Line{
				l(ctx, 1, 1), l(del, 2, 0), l(add, 0, 2), l(ctx, 3, 3)}}},
			[]Block{{OldStart: 2, OldLines: 1, NewStart: 2, NewLines: 1}},
		},
		{
			"pure insertion after old line 3",
			[]gitrepo.Hunk{{OldStart: 2, OldLines: 2, NewStart: 2, NewLines: 4, Lines: []gitrepo.Line{
				l(ctx, 2, 2), l(ctx, 3, 3), l(add, 0, 4), l(add, 0, 5)}}},
			[]Block{{OldStart: 3, OldLines: 0, NewStart: 4, NewLines: 2}},
		},
		{
			"insertion with -U0",
			[]gitrepo.Hunk{{OldStart: 3, OldLines: 0, NewStart: 4, NewLines: 2, Lines: []gitrepo.Line{
				l(add, 0, 4), l(add, 0, 5)}}},
			[]Block{{OldStart: 3, OldLines: 0, NewStart: 4, NewLines: 2}},
		},
		{
			"pure deletion",
			[]gitrepo.Hunk{{OldStart: 4, OldLines: 2, NewStart: 3, NewLines: 0, Lines: []gitrepo.Line{
				l(del, 4, 0), l(del, 5, 0)}}},
			[]Block{{OldStart: 4, OldLines: 2, NewStart: 3, NewLines: 0}},
		},
		{
			"new file",
			[]gitrepo.Hunk{{OldStart: 0, OldLines: 0, NewStart: 1, NewLines: 2, Lines: []gitrepo.Line{
				l(add, 0, 1), l(add, 0, 2)}}},
			[]Block{{OldStart: 0, OldLines: 0, NewStart: 1, NewLines: 2}},
		},
		{
			"two blocks in one hunk",
			[]gitrepo.Hunk{{OldStart: 1, OldLines: 4, NewStart: 1, NewLines: 4, Lines: []gitrepo.Line{
				l(del, 1, 0), l(add, 0, 1), l(ctx, 2, 2), l(ctx, 3, 3), l(del, 4, 0), l(add, 0, 4)}}},
			[]Block{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1}, {OldStart: 4, OldLines: 1, NewStart: 4, NewLines: 1}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ChangeBlocks(tt.hunks); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestPort(t *testing.T) {
	ins := func(after, newStart, n int) Block { return Block{OldStart: after, NewStart: newStart, NewLines: n} }
	tests := []struct {
		name       string
		blocks     []Block
		start, end int
		want       Position
	}{
		{"no blocks", nil, 5, 7, Position{Start: 5, End: 7}},
		{"block below", []Block{{OldStart: 10, OldLines: 1, NewStart: 10, NewLines: 3}}, 5, 7, Position{Start: 5, End: 7}},
		{"3 lines inserted above", []Block{ins(2, 3, 3)}, 5, 7, Position{Start: 8, End: 10}},
		{"2 lines deleted above", []Block{{OldStart: 1, OldLines: 2, NewStart: 0}}, 5, 7, Position{Start: 3, End: 5}},
		{"insertion after last commented line", []Block{ins(7, 8, 2)}, 5, 7, Position{Start: 5, End: 7}},
		{"insertion after the line before the range", []Block{ins(4, 5, 2)}, 5, 7, Position{Start: 7, End: 9}},
		{"insertion inside the range", []Block{ins(5, 6, 1)}, 5, 7, Position{FileLevel: true}},
		{"modified line in range", []Block{{OldStart: 6, OldLines: 1, NewStart: 6, NewLines: 1}}, 5, 7, Position{FileLevel: true}},
		{"modified first line", []Block{{OldStart: 5, OldLines: 1, NewStart: 5, NewLines: 1}}, 5, 7, Position{FileLevel: true}},
		{"modified last line", []Block{{OldStart: 7, OldLines: 1, NewStart: 7, NewLines: 1}}, 5, 7, Position{FileLevel: true}},
		{"range inside deleted block", []Block{{OldStart: 4, OldLines: 5, NewStart: 3}}, 5, 7, Position{FileLevel: true}},
		{"mixed blocks above", []Block{ins(0, 1, 4), {OldStart: 2, OldLines: 1, NewStart: 6}, {OldStart: 3, OldLines: 1, NewStart: 6, NewLines: 2}}, 5, 7, Position{Start: 9, End: 11}},
		{"single line", []Block{ins(0, 1, 1)}, 1, 1, Position{Start: 2, End: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Port(tt.blocks, tt.start, tt.end)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestPortErrors(t *testing.T) {
	tests := []struct {
		name       string
		blocks     []Block
		start, end int
	}{
		{"start 0", nil, 0, 1},
		{"end before start", nil, 3, 2},
		{"blocks out of order", []Block{{OldStart: 5, OldLines: 1, NewStart: 5, NewLines: 1}, {OldStart: 2, OldLines: 1, NewStart: 2, NewLines: 1}}, 10, 10},
		{"overlapping blocks", []Block{{OldStart: 2, OldLines: 3, NewStart: 2, NewLines: 3}, {OldStart: 4, OldLines: 1, NewStart: 4, NewLines: 1}}, 10, 10},
		{"negative count", []Block{{OldStart: 2, OldLines: -1, NewStart: 2, NewLines: 1}}, 10, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Port(tt.blocks, tt.start, tt.end); !errors.Is(err, ErrRange) {
				t.Fatalf("err = %v, want ErrRange", err)
			}
		})
	}
}

// edit is a change to a file of lines: replace old lines [at, at+del) with
// ins new lines. at is 1-based; a pure insertion with at = k inserts after
// line k-1.
type edit struct{ at, del, ins int }

// apply returns the edited file and the blocks describing the edits.
func apply(old []string, edits []edit) ([]string, []Block) {
	var out []string
	var blocks []Block
	next := 1
	for i, e := range edits {
		for ; next < e.at; next++ {
			out = append(out, old[next-1])
		}
		b := Block{OldStart: e.at, OldLines: e.del, NewStart: len(out) + 1, NewLines: e.ins}
		if e.del == 0 {
			b.OldStart = e.at - 1
		}
		if e.ins == 0 {
			b.NewStart = len(out)
		}
		blocks = append(blocks, b)
		for j := range e.ins {
			out = append(out, fmt.Sprintf("new-%d-%d", i, j))
		}
		next += e.del
	}
	for ; next <= len(old); next++ {
		out = append(out, old[next-1])
	}
	return out, blocks
}

// touches reports whether e changes or splits the range start..end.
func touches(e edit, start, end int) bool {
	if e.del == 0 {
		return e.at > start && e.at <= end
	}
	return e.at <= end && e.at+e.del-1 >= start
}

func TestPortProperty(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for iter := range 2000 {
		n := 1 + r.IntN(40)
		old := make([]string, n)
		for i := range old {
			old[i] = fmt.Sprintf("old-%d", i+1)
		}
		start := 1 + r.IntN(n)
		end := start + r.IntN(n-start+1)
		var edits []edit
		at := 1
		for at <= n+1 && r.IntN(3) > 0 {
			at += r.IntN(4)
			if at > n+1 {
				break
			}
			e := edit{at: at, del: min(r.IntN(3), n+1-at), ins: r.IntN(3)}
			if e.del == 0 && e.ins == 0 {
				e.ins = 1
			}
			edits = append(edits, e)
			at += e.del + 1
		}
		newFile, blocks := apply(old, edits)
		touched := slices.ContainsFunc(edits, func(e edit) bool { return touches(e, start, end) })
		got, err := Port(blocks, start, end)
		if err != nil {
			t.Fatalf("iter %d: %v (blocks %+v)", iter, err, blocks)
		}
		if touched {
			if !got.FileLevel {
				t.Fatalf("iter %d: edits %+v touch %d..%d but got %+v", iter, edits, start, end, got)
			}
			continue
		}
		if got.FileLevel {
			t.Fatalf("iter %d: edits %+v do not touch %d..%d but got file level", iter, edits, start, end)
		}
		if !slices.Equal(newFile[got.Start-1:got.End], old[start-1:end]) {
			t.Fatalf("iter %d: ported text differs; edits %+v range %d..%d got %+v", iter, edits, start, end, got)
		}
	}
}

func TestPortValidatesBlocksAfterAFileLevelResult(t *testing.T) {
	blocks := []Block{{OldStart: 5, OldLines: 1, NewStart: 5, NewLines: 1}, {OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1}}
	if _, err := Port(blocks, 5, 5); !errors.Is(err, ErrRange) {
		t.Fatalf("err = %v, want ErrRange", err)
	}
}

func FuzzPort(f *testing.F) {
	f.Add([]byte{2, 0, 3, 5, 1, 1}, 4, 6)
	f.Add([]byte{1, 2, 0, 9, 1, 1}, 1, 1)
	f.Fuzz(func(t *testing.T, raw []byte, start, end int) {
		// Each 3 bytes are one block: gap before it, old lines, new lines.
		var blocks []Block
		old, nw := 0, 0
		for i := 0; i+2 < len(raw); i += 3 {
			gap, del, ins := int(raw[i]%8), int(raw[i+1]%4), int(raw[i+2]%4)
			old += gap
			nw += gap
			b := Block{OldStart: old + 1, OldLines: del, NewStart: nw + 1, NewLines: ins}
			if del == 0 {
				b.OldStart = old
			}
			if ins == 0 {
				b.NewStart = nw
			}
			blocks = append(blocks, b)
			old += del
			nw += ins
		}
		got, err := Port(blocks, start, end)
		if err != nil {
			if start >= 1 && end >= start && ordered(blocks) {
				t.Fatalf("unexpected error %v", err)
			}
			return
		}
		if !got.FileLevel && (got.Start < 1 || got.End-got.Start != end-start) {
			t.Fatalf("Port(%+v, %d, %d) = %+v", blocks, start, end, got)
		}
	})
}
