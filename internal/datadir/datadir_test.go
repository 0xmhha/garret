package datadir

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newDir(t *testing.T) *Dir {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestOpenCreatesLayout(t *testing.T) {
	d := newDir(t)
	for _, p := range []string{d.Root(), d.ReposDir(), d.AttachmentsDir(), d.TLSDir()} {
		if m := mode(t, p); m != 0o700 {
			t.Errorf("%s mode %o, want 700", p, m)
		}
	}
	if !filepath.IsAbs(d.Root()) {
		t.Errorf("root %q is not absolute", d.Root())
	}
	if d.DBPath() != filepath.Join(d.Root(), "garret.db") {
		t.Errorf("DBPath %q", d.DBPath())
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	d := newDir(t)
	if _, err := Open(d.Root()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRejectsOpenPermissions(t *testing.T) {
	tests := []struct {
		name string
		path func(d *Dir) string
		mode os.FileMode
	}{
		{"root readable by others", func(d *Dir) string { return d.Root() }, 0o755},
		{"root readable by group", func(d *Dir) string { return d.Root() }, 0o740},
		{"repos readable by group", func(d *Dir) string { return d.ReposDir() }, 0o750},
		{"tls writable by others", func(d *Dir) string { return d.TLSDir() }, 0o702},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDir(t)
			p := tt.path(d)
			if err := os.Chmod(p, tt.mode); err != nil {
				t.Fatal(err)
			}
			_, err := Open(d.Root())
			if !errors.Is(err, ErrPermissions) {
				t.Fatalf("err = %v, want ErrPermissions", err)
			}
			if m := mode(t, p); m != tt.mode {
				t.Fatalf("Open changed the mode to %o", m)
			}
		})
	}
}

func TestOpenRejectsSymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link); !errors.Is(err, ErrSymlink) {
		t.Fatalf("err = %v, want ErrSymlink", err)
	}
	d := newDir(t)
	if err := os.Remove(d.TLSDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, d.TLSDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(d.Root()); !errors.Is(err, ErrSymlink) {
		t.Fatalf("subdirectory symlink: err = %v, want ErrSymlink", err)
	}
}

func TestOpenRejectsFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p); err == nil {
		t.Fatal("opened a regular file as the data directory")
	}
}

func TestWriteFile(t *testing.T) {
	d := newDir(t)
	if err := d.WriteFile("tls/ca.crt", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := d.WriteFile("tls/ca.crt", []byte("two")); err != nil {
		t.Fatal(err)
	}
	got, err := d.ReadFile("tls/ca.crt")
	if err != nil || !bytes.Equal(got, []byte("two")) {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}
	if m := mode(t, filepath.Join(d.TLSDir(), "ca.crt")); m != 0o600 {
		t.Fatalf("mode %o, want 600", m)
	}
	assertNoTemp(t, d.TLSDir())
}

func TestWriteFileRejectsEscapes(t *testing.T) {
	d := newDir(t)
	for _, rel := range []string{"../x", "/etc/x", "tls/../../x", "", "."} {
		if err := d.WriteFile(rel, []byte("x")); !errors.Is(err, ErrPath) {
			t.Errorf("WriteFile(%q) = %v, want ErrPath", rel, err)
		}
		if _, err := d.ReadFile(rel); !errors.Is(err, ErrPath) {
			t.Errorf("ReadFile(%q) = %v, want ErrPath", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(d.Root()), "x")); !os.IsNotExist(err) {
		t.Fatal("a file was written outside the data directory")
	}
}

func TestWriteFileFailureKeepsOldContent(t *testing.T) {
	d := newDir(t)
	if err := d.WriteFile("tls/server.crt", []byte("old")); err != nil {
		t.Fatal(err)
	}
	// A directory in the way makes the rename fail.
	if err := os.Mkdir(filepath.Join(d.TLSDir(), "blocked"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.TLSDir(), "blocked", "child"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.WriteFile("tls/blocked", []byte("new")); err == nil {
		t.Fatal("WriteFile over a non-empty directory succeeded")
	}
	assertNoTemp(t, d.TLSDir())
	got, err := d.ReadFile("tls/server.crt")
	if err != nil || string(got) != "old" {
		t.Fatalf("other file changed: %q, %v", got, err)
	}
}

func assertNoTemp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if len(e.Name()) > 0 && e.Name()[0] == '.' {
			t.Fatalf("temporary file %s left behind", e.Name())
		}
	}
}
