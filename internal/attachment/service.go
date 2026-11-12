package attachment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/clock"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/store"
	"github.com/0xmhha/garret/internal/store/sqlcgen"
)

// Service errors.
var (
	ErrNotFound  = errors.New("attachment: not found")
	ErrForbidden = errors.New("attachment: not allowed")
)

const (
	maxFilenameBytes = 255
	fallbackFilename = "file"
)

// Attachment is an uploaded file's metadata.
type Attachment struct {
	ID         string
	ProjectID  string
	UploaderID string
	Filename   string // display only, never used as a path
	MediaType  string // detected from the content
	Size       int64
	Inline     bool // may be shown in the page; otherwise served as a download
	CreatedAt  time.Time
}

// Service stores attachments and their metadata. It is safe for concurrent
// use.
type Service struct {
	db      *store.DB
	audit   *audit.Recorder
	ids     *id.Generator
	clock   clock.Clock
	files   *Store
	maxSize int64
}

// NewService returns an attachment Service with an upload limit of maxSize
// bytes (DefaultMaxSize in production).
func NewService(db *store.DB, rec *audit.Recorder, ids *id.Generator, c clock.Clock, files *Store, maxSize int64) *Service {
	return &Service{db: db, audit: rec, ids: ids, clock: c, files: files, maxSize: maxSize}
}

// Upload stores r as an attachment of the project. The filename is kept for
// display only, stripped of path separators and control characters.
func (s *Service) Upload(ctx context.Context, actor auth.User, projectName, filename string, r io.Reader) (Attachment, error) {
	projectID, err := s.projectID(ctx, projectName)
	if err != nil {
		return Attachment{}, err
	}
	stored, err := s.files.Put(projectID, r, s.maxSize)
	if err != nil {
		return Attachment{}, err
	}
	attachmentID, err := s.ids.New()
	if err != nil {
		return Attachment{}, err
	}
	now := s.clock.Now().UnixMilli()
	a := Attachment{ID: attachmentID, ProjectID: projectID, UploaderID: actor.ID, Filename: cleanFilename(filename),
		MediaType: stored.MediaType, Size: stored.Size, Inline: Inline(stored.MediaType), CreatedAt: time.UnixMilli(now).UTC()}
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := sqlcgen.New(tx).InsertAttachment(ctx, sqlcgen.InsertAttachmentParams{ID: attachmentID, ProjectID: projectID,
			UploaderID: actor.ID, Filename: a.Filename, MediaType: a.MediaType, Size: a.Size, Sha256: stored.SHA256[:],
			CreatedAt: now}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ProjectID: projectID, ActorID: actor.ID, Action: "attachment.uploaded",
			SubjectKind: "attachment", SubjectID: attachmentID, Data: map[string]any{"size": a.Size, "media_type": a.MediaType}})
	})
	return a, err
}

// Open returns an attachment that is not deleted, with its file opened for
// reading. The caller closes the file.
func (s *Service) Open(ctx context.Context, _ auth.User, attachmentID string) (Attachment, *os.File, error) {
	var row sqlcgen.Attachment
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		row, err = sqlcgen.New(tx).GetAttachment(ctx, attachmentID)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) || err == nil && row.DeletedAt.Valid {
		return Attachment{}, nil, ErrNotFound
	}
	if err != nil {
		return Attachment{}, nil, err
	}
	var sum [32]byte
	copy(sum[:], row.Sha256)
	f, err := s.files.Open(row.ProjectID, sum)
	if err != nil {
		return Attachment{}, nil, fmt.Errorf("attachment: open file: %w", err)
	}
	return toAttachment(row), f, nil
}

// Delete soft-deletes an attachment. The uploader or an administrator may
// delete; the file stays until erasure or purge.
func (s *Service) Delete(ctx context.Context, actor auth.User, attachmentID string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		a, err := q.GetAttachment(ctx, attachmentID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if a.UploaderID != actor.ID && !actor.IsAdmin() {
			return ErrForbidden
		}
		if _, err := q.SoftDeleteAttachment(ctx, sqlcgen.SoftDeleteAttachmentParams{
			DeletedAt: sql.NullInt64{Int64: s.clock.Now().UnixMilli(), Valid: true}, ID: attachmentID}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ProjectID: a.ProjectID, ActorID: actor.ID, Action: "attachment.deleted",
			SubjectKind: "attachment", SubjectID: attachmentID})
	})
}

func (s *Service) projectID(ctx context.Context, name string) (string, error) {
	var p sqlcgen.Project
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		p, err = sqlcgen.New(tx).GetProjectByName(ctx, name)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: project", ErrNotFound)
	}
	return p.ID, err
}

// cleanFilename removes path separators and control characters, cuts the
// name to 255 bytes on a character boundary, and falls back to "file".
func cleanFilename(name string) string {
	name = strings.ToValidUTF8(name, "")
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	for len(name) > maxFilenameBytes {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	if name == "" {
		return fallbackFilename
	}
	return name
}

func toAttachment(a sqlcgen.Attachment) Attachment {
	return Attachment{ID: a.ID, ProjectID: a.ProjectID, UploaderID: a.UploaderID, Filename: a.Filename,
		MediaType: a.MediaType, Size: a.Size, Inline: Inline(a.MediaType), CreatedAt: time.UnixMilli(a.CreatedAt).UTC()}
}
