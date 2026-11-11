package gitrepo

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const (
	sha1a = "bdc955b7b2e610ad5a72302b139a2e6cb325519a"
	sha1b = "8835708590a9afa236e1bbad18df9d23de82ccd3"
	zero  = "0000000000000000000000000000000000000000"
)

func rawRec(meta string, paths ...string) string {
	return ":" + meta + "\x00" + strings.Join(paths, "\x00") + "\x00"
}

func TestParseRaw(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []FileChange
	}{
		{"empty", "", nil},
		{
			"modify add delete",
			rawRec("100644 100644 "+sha1a+" "+sha1b+" M", "a.go") +
				rawRec("000000 100644 "+zero+" "+sha1b+" A", "new.go") +
				rawRec("100644 000000 "+sha1a+" "+zero+" D", "old.go"),
			[]FileChange{
				{Status: StatusModified, OldPath: "a.go", NewPath: "a.go", OldMode: "100644", NewMode: "100644", OldSHA: sha1a, NewSHA: sha1b},
				{Status: StatusAdded, NewPath: "new.go", OldMode: "000000", NewMode: "100644", OldSHA: zero, NewSHA: sha1b},
				{Status: StatusDeleted, OldPath: "old.go", OldMode: "100644", NewMode: "000000", OldSHA: sha1a, NewSHA: zero},
			},
		},
		{
			"rename",
			rawRec("100644 100644 "+sha1a+" "+sha1b+" R087", "f.txt", "g.txt"),
			[]FileChange{{Status: StatusRenamed, OldPath: "f.txt", NewPath: "g.txt", OldMode: "100644", NewMode: "100644", OldSHA: sha1a, NewSHA: sha1b, Similarity: 87}},
		},
		{
			"unusual path bytes",
			rawRec("100644 100755 "+sha1a+" "+sha1a+" M", "sp ace\n\"q\"\xff.txt"),
			[]FileChange{{Status: StatusModified, OldPath: "sp ace\n\"q\"\xff.txt", NewPath: "sp ace\n\"q\"\xff.txt", OldMode: "100644", NewMode: "100755", OldSHA: sha1a, NewSHA: sha1a}},
		},
		{
			"type change",
			rawRec("120000 100644 "+sha1a+" "+sha1b+" T", "link"),
			[]FileChange{{Status: StatusTypeChanged, OldPath: "link", NewPath: "link", OldMode: "120000", NewMode: "100644", OldSHA: sha1a, NewSHA: sha1b}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRaw([]byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestParseRawErrors(t *testing.T) {
	meta := "100644 100644 " + sha1a + " " + sha1b
	tests := map[string]string{
		"missing colon":     strings.TrimPrefix(rawRec(meta+" M", "a"), ":"),
		"missing final NUL": strings.TrimSuffix(rawRec(meta+" M", "a"), "\x00"),
		"missing path":      ":" + meta + " M\x00",
		"empty path":        rawRec(meta+" M", ""),
		"rename one path":   rawRec(meta+" R100", "a"),
		"bad mode":          rawRec("10064x 100644 "+sha1a+" "+sha1b+" M", "a"),
		"short mode":        rawRec("10644 100644 "+sha1a+" "+sha1b+" M", "a"),
		"bad sha":           rawRec("100644 100644 XYZ1 "+sha1b+" M", "a"),
		"unknown status":    rawRec(meta+" X", "a"),
		"copy":              rawRec(meta+" C075", "a", "b"),
		"unmerged":          rawRec(meta+" U", "a"),
		"score on modify":   rawRec(meta+" M100", "a"),
		"similarity 101":    rawRec(meta+" R101", "a", "b"),
		"too few fields":    rawRec("100644 "+sha1a+" "+sha1b+" M", "a"),
		"empty status":      rawRec(meta+" ", "a"),
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseRaw([]byte(in)); !errors.Is(err, ErrRawFormat) {
				t.Fatalf("err = %v, want ErrRawFormat", err)
			}
		})
	}
}

func TestParseRawErrorHasOffset(t *testing.T) {
	good := rawRec("100644 100644 "+sha1a+" "+sha1b+" M", "a")
	_, err := ParseRaw([]byte(good + rawRec("100644 100644 "+sha1a+" "+sha1b+" X", "b")))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("at byte %d", len(good))) {
		t.Fatalf("err = %v, want offset %d", err, len(good))
	}
}

// formatRaw writes changes back in git's --raw -z form, for round trips.
func formatRaw(changes []FileChange) []byte {
	var b bytes.Buffer
	for _, c := range changes {
		status := string(rune(c.Status))
		if c.Status == StatusRenamed {
			status = fmt.Sprintf("R%03d", c.Similarity)
		}
		fmt.Fprintf(&b, ":%s %s %s %s %s\x00", c.OldMode, c.NewMode, c.OldSHA, c.NewSHA, status)
		switch c.Status {
		case StatusAdded:
			b.WriteString(c.NewPath + "\x00")
		case StatusRenamed:
			b.WriteString(c.OldPath + "\x00" + c.NewPath + "\x00")
		default:
			b.WriteString(c.OldPath + "\x00")
		}
	}
	return b.Bytes()
}

func FuzzParseRaw(f *testing.F) {
	f.Add([]byte(rawRec("100644 100644 "+sha1a+" "+sha1b+" M", "a.go")))
	f.Add([]byte(rawRec("100644 100644 "+sha1a+" "+sha1b+" R087", "f", "g")))
	f.Add([]byte(rawRec("000000 100644 "+zero+" "+sha1b+" A", "x") + rawRec("100644 000000 "+sha1a+" "+zero+" D", "y")))
	f.Fuzz(func(t *testing.T, in []byte) {
		got, err := ParseRaw(in)
		if err != nil {
			return
		}
		if again := formatRaw(got); !bytes.Equal(again, in) {
			t.Fatalf("round trip changed input:\n in  %q\n out %q", in, again)
		}
	})
}

func TestParseNumstat(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []LineCount
	}{
		{"empty", "", nil},
		{"text", "2\t1\ta.go\x00", []LineCount{{Added: 2, Deleted: 1, OldPath: "a.go", NewPath: "a.go"}}},
		{"binary", "-\t-\tbin.dat\x00", []LineCount{{Binary: true, OldPath: "bin.dat", NewPath: "bin.dat"}}},
		{"rename", "2\t1\t\x00f.txt\x00g.txt\x00", []LineCount{{Added: 2, Deleted: 1, OldPath: "f.txt", NewPath: "g.txt"}}},
		{"path with tab", "0\t0\ta\tb\x00", []LineCount{{OldPath: "a\tb", NewPath: "a\tb"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseNumstat([]byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseNumstatErrors(t *testing.T) {
	for name, in := range map[string]string{
		"one dash":         "-\t1\ta\x00",
		"negative":         "-1\t1\ta\x00",
		"not a number":     "x\t1\ta\x00",
		"missing field":    "1\ta\x00",
		"no NUL":           "1\t1\ta",
		"rename one path":  "1\t1\t\x00a\x00",
		"rename empty new": "1\t1\t\x00a\x00\x00",
		"huge count":       "99999999999999999999\t1\ta\x00",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseNumstat([]byte(in)); !errors.Is(err, ErrRawFormat) {
				t.Fatalf("err = %v, want ErrRawFormat", err)
			}
		})
	}
}

func FuzzParseNumstat(f *testing.F) {
	f.Add([]byte("2\t1\ta.go\x00-\t-\tbin\x00"))
	f.Add([]byte("2\t1\t\x00f\x00g\x00"))
	f.Fuzz(func(t *testing.T, in []byte) {
		got, err := ParseNumstat(in)
		if err != nil {
			return
		}
		for _, c := range got {
			if c.Added < 0 || c.Deleted < 0 || c.OldPath == "" || c.NewPath == "" {
				t.Fatalf("accepted invalid record %+v", c)
			}
		}
	})
}
