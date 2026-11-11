package gitrepo

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

var testLimits = Limits{MaxBytes: 1 << 20, MaxLines: 1000}

const header = "diff --git a/f b/f\nindex 1111111..2222222 100644\n--- a/f\n+++ b/f\n"

func TestParseHunks(t *testing.T) {
	tests := []struct {
		name  string
		patch string
		want  []Hunk
	}{
		{"empty", "", nil},
		{"headers only", header, nil},
		{
			"one change with context",
			header + "@@ -1,3 +1,3 @@ func main() {\n a\n-b\n+B\n c\n",
			[]Hunk{{OldStart: 1, OldLines: 3, NewStart: 1, NewLines: 3, Section: "func main() {", Lines: []Line{
				{Kind: LineContext, Text: []byte("a"), OldNo: 1, NewNo: 1},
				{Kind: LineDeleted, Text: []byte("b"), OldNo: 2},
				{Kind: LineAdded, Text: []byte("B"), NewNo: 2},
				{Kind: LineContext, Text: []byte("c"), OldNo: 3, NewNo: 3},
			}}},
		},
		{
			"new file",
			"@@ -0,0 +1,3 @@\n+a\n+b\n+c\n",
			[]Hunk{{OldStart: 0, OldLines: 0, NewStart: 1, NewLines: 3, Lines: []Line{
				{Kind: LineAdded, Text: []byte("a"), NewNo: 1},
				{Kind: LineAdded, Text: []byte("b"), NewNo: 2},
				{Kind: LineAdded, Text: []byte("c"), NewNo: 3},
			}}},
		},
		{
			"deleted file",
			"@@ -1,2 +0,0 @@\n-a\n-b\n",
			[]Hunk{{OldStart: 1, OldLines: 2, NewStart: 0, NewLines: 0, Lines: []Line{
				{Kind: LineDeleted, Text: []byte("a"), OldNo: 1},
				{Kind: LineDeleted, Text: []byte("b"), OldNo: 2},
			}}},
		},
		{
			"omitted counts",
			"@@ -5 +5 @@\n-x\n+y\n",
			[]Hunk{{OldStart: 5, OldLines: 1, NewStart: 5, NewLines: 1, Lines: []Line{
				{Kind: LineDeleted, Text: []byte("x"), OldNo: 5},
				{Kind: LineAdded, Text: []byte("y"), NewNo: 5},
			}}},
		},
		{
			"no newline on new side",
			"@@ -1 +1 @@\n-a\n+a\n\\ No newline at end of file\n",
			[]Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Lines: []Line{
				{Kind: LineDeleted, Text: []byte("a"), OldNo: 1},
				{Kind: LineAdded, Text: []byte("a"), NewNo: 1, NoNewlineAtEOF: true},
			}}},
		},
		{
			"content that looks like markers",
			"@@ -1,2 +1,2 @@\n @@ not a header\n-\\ text\n+\\ text\n",
			[]Hunk{{OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 2, Lines: []Line{
				{Kind: LineContext, Text: []byte("@@ not a header"), OldNo: 1, NewNo: 1},
				{Kind: LineDeleted, Text: []byte("\\ text"), OldNo: 2},
				{Kind: LineAdded, Text: []byte("\\ text"), NewNo: 2},
			}}},
		},
		{
			"CRLF kept",
			"@@ -1 +1 @@\n-a\r\n+b\r\n",
			[]Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Lines: []Line{
				{Kind: LineDeleted, Text: []byte("a\r"), OldNo: 1},
				{Kind: LineAdded, Text: []byte("b\r"), NewNo: 1},
			}}},
		},
		{
			"two hunks",
			"@@ -1 +1 @@\n-a\n+b\n@@ -10,0 +11,1 @@\n+z\n",
			[]Hunk{
				{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Lines: []Line{
					{Kind: LineDeleted, Text: []byte("a"), OldNo: 1},
					{Kind: LineAdded, Text: []byte("b"), NewNo: 1},
				}},
				{OldStart: 10, OldLines: 0, NewStart: 11, NewLines: 1, Lines: []Line{
					{Kind: LineAdded, Text: []byte("z"), NewNo: 11},
				}},
			},
		},
		{
			"missing final newline in patch",
			"@@ -1 +1 @@\n-a\n+b",
			[]Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Lines: []Line{
				{Kind: LineDeleted, Text: []byte("a"), OldNo: 1},
				{Kind: LineAdded, Text: []byte("b"), NewNo: 1},
			}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseHunks([]byte(tt.patch), testLimits)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestParseHunksErrors(t *testing.T) {
	tests := map[string]string{
		"too few lines":          "@@ -1,2 +1,2 @@\n a\n",
		"too many lines":         "@@ -1 +1 @@\n a\n b\n",
		"unknown marker":         "@@ -1 +1 @@\n*a\n",
		"empty line in hunk":     "@@ -1,2 +1,2 @@\n a\n\n",
		"negative start":         "@@ --1 +1 @@\n a\n",
		"bad header":             "@@ -1 +1\n a\n",
		"nonzero start no lines": "@@ -1,1 +0,1 @@\n a\n",
		"zero start with lines":  "@@ -0,1 +0,1 @@\n a\n",
		"overlapping hunks":      "@@ -1,2 +1,2 @@\n a\n b\n@@ -2 +2 @@\n b\n",
		"out of order":           "@@ -5 +5 @@\n a\n@@ -1 +1 @@\n b\n",
		"marker first":           "@@ -1 +1 @@\n\\ No newline at end of file\n a\n",
		"text after hunk":        "@@ -1 +1 @@\n a\ndiff --git a/g b/g\n",
		"huge number":            "@@ -99999999999 +1 @@\n a\n",
	}
	for name, patch := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseHunks([]byte(patch), testLimits); !errors.Is(err, ErrPatchFormat) {
				t.Fatalf("err = %v, want ErrPatchFormat", err)
			}
		})
	}
}

func TestParseHunksLimits(t *testing.T) {
	patch := "@@ -1,3 +1,3 @@\n a\n b\n c\n"
	tests := []struct {
		name   string
		limits Limits
		want   error
	}{
		{"at byte limit", Limits{MaxBytes: len(patch), MaxLines: 10}, nil},
		{"one byte over", Limits{MaxBytes: len(patch) - 1, MaxLines: 10}, ErrTooLarge},
		{"at line limit", Limits{MaxBytes: 1000, MaxLines: 3}, nil},
		{"one line over", Limits{MaxBytes: 1000, MaxLines: 2}, ErrTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseHunks([]byte(patch), tt.limits); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func FuzzParseHunks(f *testing.F) {
	f.Add([]byte(header + "@@ -1,3 +1,3 @@ x\n a\n-b\n+B\n c\n"))
	f.Add([]byte("@@ -0,0 +1,2 @@\n+a\n+b\n\\ No newline at end of file\n"))
	f.Add([]byte("@@ -1 +1 @@\n-a\n+b\n@@ -10,0 +11 @@\n+z\n"))
	f.Fuzz(func(t *testing.T, patch []byte) {
		hunks, err := ParseHunks(patch, testLimits)
		if err != nil {
			return
		}
		for _, h := range hunks {
			var old, nw int
			for _, l := range h.Lines {
				if l.Kind != LineAdded {
					old++
				}
				if l.Kind != LineDeleted {
					nw++
				}
				if strings.ContainsRune(string(l.Text), '\n') {
					t.Fatalf("line text contains a newline: %q", l.Text)
				}
			}
			if old != h.OldLines || nw != h.NewLines {
				t.Fatalf("counts %d,%d do not match header %+v", old, nw, h)
			}
		}
	})
}
