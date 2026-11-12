package attachment

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const project = "01JD0000000000000000000000"

var png = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "attachments")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return New(root), root
}

func TestPutAndOpen(t *testing.T) {
	s, root := newStore(t)
	got, err := s.Put(project, bytes.NewReader(png), 1024)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(png)
	if got.SHA256 != want || got.Size != int64(len(png)) || got.MediaType != "image/png" {
		t.Fatalf("Put = %+v", got)
	}
	path := filepath.Join(root, project, hex.EncodeToString(want[:]))
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("stored file: %v, mode %v", err, fi.Mode())
	}
	f, err := s.Open(project, want)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	content, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(content, png) {
		t.Fatalf("Open content %q, %v", content, err)
	}
	assertNoTemp(t, filepath.Join(root, project))
}

func TestPutSameContentKeepsOneFile(t *testing.T) {
	s, root := newStore(t)
	for range 2 {
		if _, err := s.Put(project, strings.NewReader("same"), 100); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, project))
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries %v, %v", entries, err)
	}
}

func TestPutLimits(t *testing.T) {
	s, root := newStore(t)
	if _, err := s.Put(project, strings.NewReader("12345"), 5); err != nil {
		t.Fatalf("exactly at the limit: %v", err)
	}
	if _, err := s.Put(project, strings.NewReader("123456"), 5); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the limit: %v", err)
	}
	assertNoTemp(t, filepath.Join(root, project))
	got, err := s.Put(project, strings.NewReader(""), 5)
	if err != nil || got.Size != 0 || got.MediaType != "text/plain; charset=utf-8" {
		t.Fatalf("empty file: %+v, %v", got, err)
	}
}

var errRead = errors.New("read failed")

type failingReader struct{ n int }

func (r *failingReader) Read(p []byte) (int, error) {
	if r.n == 0 {
		return 0, errRead
	}
	r.n--
	p[0] = 'x'
	return 1, nil
}

func TestPutReadFailureLeavesNothing(t *testing.T) {
	s, root := newStore(t)
	if _, err := s.Put(project, &failingReader{n: 3}, 100); !errors.Is(err, errRead) {
		t.Fatalf("err = %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, project))
	if len(entries) != 0 {
		t.Fatalf("left %v", entries)
	}
}

func TestProjectIDChecked(t *testing.T) {
	s, root := newStore(t)
	for _, id := range []string{"", "..", "../x", "01jd0000000000000000000000", "not-a-ulid", project + "/x"} {
		if _, err := s.Put(id, strings.NewReader("x"), 10); !errors.Is(err, ErrProjectID) {
			t.Errorf("Put(%q) = %v, want ErrProjectID", id, err)
		}
		if _, err := s.Open(id, [32]byte{}); !errors.Is(err, ErrProjectID) {
			t.Errorf("Open(%q) = %v", id, err)
		}
		if err := s.RemoveProject(id); !errors.Is(err, ErrProjectID) {
			t.Errorf("RemoveProject(%q) = %v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "x")); !os.IsNotExist(err) {
		t.Fatal("wrote outside the store")
	}
}

func TestRemoveProject(t *testing.T) {
	s, root := newStore(t)
	other := "01JD0000000000000000000001"
	for _, p := range []string{project, other} {
		if _, err := s.Put(p, strings.NewReader("x"), 10); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RemoveProject(project); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, project)); !os.IsNotExist(err) {
		t.Fatal("project directory still exists")
	}
	if _, err := os.Stat(filepath.Join(root, other)); err != nil {
		t.Fatal("another project's files were removed")
	}
	if err := s.RemoveProject(project); err != nil {
		t.Fatalf("removing twice: %v", err)
	}
}

func TestOpenMissing(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.Open(project, [32]byte{1}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}

func TestInline(t *testing.T) {
	for mt, want := range map[string]bool{
		"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
		"image/svg+xml": false, "text/html; charset=utf-8": false, "application/pdf": false, "": false,
	} {
		if got := Inline(mt); got != want {
			t.Errorf("Inline(%q) = %v, want %v", mt, got, want)
		}
	}
}

func assertNoTemp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("temporary file %s left", e.Name())
		}
	}
}
