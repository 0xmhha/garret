package datadir

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

var (
	// ErrPermissions is returned when a directory grants access to group or
	// others, or belongs to another user.
	ErrPermissions = errors.New("datadir: directory is not private to this user")
	// ErrSymlink is returned when the data directory or a subdirectory is a
	// symbolic link.
	ErrSymlink = errors.New("datadir: symbolic links are not allowed")
	// ErrPath is returned for a relative path that is empty or leaves the
	// data directory.
	ErrPath = errors.New("datadir: path must stay inside the data directory")
)

const (
	dirMode  os.FileMode = 0o700
	fileMode os.FileMode = 0o600
	// privateMask covers every group and other permission bit.
	privateMask os.FileMode = 0o077

	dbFile         = "garret.db"
	reposDir       = "repos"
	attachmentsDir = "attachments"
	tlsDir         = "tls"
	tempPattern    = ".tmp-*"
)

var subdirs = []string{reposDir, attachmentsDir, tlsDir}

// Dir is an opened data directory. Its methods are safe for concurrent use.
type Dir struct {
	root string
}

// Open creates the data directory at path with its layout if needed, then
// checks that the directory and every subdirectory are real directories,
// owned by the current user and closed to group and others. It never
// changes the mode of an existing directory.
func Open(path string) (*Dir, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("datadir: resolve %s: %w", path, err)
	}
	if err := ensureDir(root); err != nil {
		return nil, err
	}
	for _, sub := range subdirs {
		if err := ensureDir(filepath.Join(root, sub)); err != nil {
			return nil, err
		}
	}
	return &Dir{root: root}, nil
}

func ensureDir(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.Mkdir(path, dirMode); err != nil {
			return fmt.Errorf("datadir: create %s: %w", path, err)
		}
		fi, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("datadir: inspect %s: %w", path, err)
	}
	return checkDir(path, fi)
}

func checkDir(path string, fi fs.FileInfo) error {
	if fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s", ErrSymlink, path)
	}
	if !fi.IsDir() {
		return fmt.Errorf("datadir: %s is not a directory", path)
	}
	if perm := fi.Mode().Perm(); perm&privateMask != 0 {
		return fmt.Errorf("%w: %s has mode %o; run chmod 700 %s", ErrPermissions, path, perm, path)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%w: %s is owned by user %d", ErrPermissions, path, st.Uid)
	}
	return nil
}

// Root returns the absolute path of the data directory.
func (d *Dir) Root() string { return d.root }

// DBPath returns the path of the SQLite database.
func (d *Dir) DBPath() string { return filepath.Join(d.root, dbFile) }

// ReposDir returns the directory that holds the bare repositories.
func (d *Dir) ReposDir() string { return filepath.Join(d.root, reposDir) }

// AttachmentsDir returns the directory that holds attachment files.
func (d *Dir) AttachmentsDir() string { return filepath.Join(d.root, attachmentsDir) }

// TLSDir returns the directory that holds keys and certificates.
func (d *Dir) TLSDir() string { return filepath.Join(d.root, tlsDir) }

// WriteFile replaces the file at rel, relative to the data directory, with
// data and mode 0600. It writes a temporary file in the same directory,
// syncs it and renames it into place, so a failure leaves the previous
// content intact and no temporary file behind.
func (d *Dir) WriteFile(rel string, data []byte) (err error) {
	target, err := d.resolve(rel)
	if err != nil {
		return err
	}
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		return fmt.Errorf("datadir: create temporary file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Chmod(fileMode)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("datadir: write %s: %w", rel, err)
	}
	if err = os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("datadir: replace %s: %w", rel, err)
	}
	return syncDir(dir)
}

// ReadFile reads the file at rel, relative to the data directory.
func (d *Dir) ReadFile(rel string) ([]byte, error) {
	target, err := d.resolve(rel)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(target) // #nosec G304 -- target is confined by resolve
	if err != nil {
		return nil, fmt.Errorf("datadir: read %s: %w", rel, err)
	}
	return data, nil
}

func (d *Dir) resolve(rel string) (string, error) {
	if rel == "" || rel == "." || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%w: %q", ErrPath, rel)
	}
	return filepath.Join(d.root, rel), nil
}

func syncDir(dir string) error {
	f, err := os.Open(dir) // #nosec G304 -- dir is inside the data directory
	if err != nil {
		return fmt.Errorf("datadir: open %s: %w", dir, err)
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("datadir: sync %s: %w", dir, err)
	}
	return nil
}
