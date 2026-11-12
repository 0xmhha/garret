package attachment

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/oklog/ulid/v2"
)

var (
	// ErrTooLarge is returned when an upload exceeds the size limit.
	ErrTooLarge = errors.New("attachment: file too large")
	// ErrProjectID is returned for a project ID that is not a ULID.
	ErrProjectID = errors.New("attachment: invalid project ID")
)

// DefaultMaxSize is the upload limit (docs/design/api.md).
const DefaultMaxSize = 25 << 20

const (
	dirMode     os.FileMode = 0o700
	fileMode    os.FileMode = 0o600
	sniffBytes              = 512
	tempPattern             = ".upload-*"
)

// inlineTypes are shown in the page; everything else is a download
// (docs/specs/components.md, Q8).
var inlineTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// Store keeps files under root/<project-id>/<sha256-hex>. It is safe for
// concurrent use: writes go through a temporary file and a rename.
type Store struct {
	root string
}

// New returns a Store rooted at root, which must exist.
func New(root string) *Store { return &Store{root: root} }

// Stored describes a file after Put.
type Stored struct {
	SHA256    [sha256.Size]byte
	Size      int64
	MediaType string
}

// Put stores the content of r for projectID. It returns ErrTooLarge, and
// stores nothing, when r has more than max bytes. The media type is
// detected from the content, not taken from the client. Storing the same
// content twice keeps one file.
func (s *Store) Put(projectID string, r io.Reader, max int64) (_ Stored, err error) {
	dir, err := s.projectDir(projectID)
	if err != nil {
		return Stored{}, err
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return Stored{}, fmt.Errorf("attachment: create directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		return Stored{}, fmt.Errorf("attachment: create temporary file: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmp.Name())
		}
	}()

	h := sha256.New()
	sniff := &prefix{limit: sniffBytes}
	n, err := io.Copy(io.MultiWriter(tmp, h, sniff), io.LimitReader(r, max+1))
	if err == nil && n > max {
		err = ErrTooLarge
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		if errors.Is(err, ErrTooLarge) {
			return Stored{}, err
		}
		return Stored{}, fmt.Errorf("attachment: write: %w", err)
	}

	out := Stored{Size: n, MediaType: http.DetectContentType(sniff.buf)}
	copy(out.SHA256[:], h.Sum(nil))
	target := filepath.Join(dir, hex.EncodeToString(out.SHA256[:]))
	if _, statErr := os.Stat(target); statErr == nil {
		return out, nil // same content already stored
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return Stored{}, fmt.Errorf("attachment: store: %w", err)
	}
	keep = true
	return out, nil
}

// Open opens the file with the given digest in projectID.
func (s *Store) Open(projectID string, sum [sha256.Size]byte) (*os.File, error) {
	dir, err := s.projectDir(projectID)
	if err != nil {
		return nil, err
	}
	return os.Open(filepath.Join(dir, hex.EncodeToString(sum[:]))) // #nosec G304 -- path built from a ULID and a hex digest
}

// RemoveProject removes every file of projectID. Removing a project that
// has no files is not an error.
func (s *Store) RemoveProject(projectID string) error {
	dir, err := s.projectDir(projectID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("attachment: remove project files: %w", err)
	}
	return nil
}

// Inline reports whether a file of mediaType may be shown in the page.
func Inline(mediaType string) bool { return inlineTypes[mediaType] }

func (s *Store) projectDir(projectID string) (string, error) {
	// ParseStrict accepts lowercase too; garret only writes the canonical form.
	if u, err := ulid.ParseStrict(projectID); err != nil || u.String() != projectID {
		return "", ErrProjectID
	}
	return filepath.Join(s.root, projectID), nil
}

// prefix keeps the first limit bytes written to it.
type prefix struct {
	buf   []byte
	limit int
}

func (p *prefix) Write(b []byte) (int, error) {
	if room := p.limit - len(p.buf); room > 0 {
		p.buf = append(p.buf, b[:min(room, len(b))]...)
	}
	return len(b), nil
}

// Remove deletes the file with the given digest from projectID. Removing a
// file that does not exist is not an error.
func (s *Store) Remove(projectID string, sum [sha256.Size]byte) error {
	dir, err := s.projectDir(projectID)
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, hex.EncodeToString(sum[:]))); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("attachment: remove file: %w", err)
	}
	return nil
}

// Root returns the directory the store keeps project directories in.
func (s *Store) Root() string { return s.root }
