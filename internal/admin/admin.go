package admin

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/0xmhha/garret/internal/attachment"
	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/clock"
	"github.com/0xmhha/garret/internal/collab"
	"github.com/0xmhha/garret/internal/gitrepo"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/project"
	"github.com/0xmhha/garret/internal/store"
	"github.com/0xmhha/garret/internal/store/sqlcgen"
)

// Service errors.
var (
	ErrNotFound       = errors.New("admin: not found")
	ErrForbidden      = errors.New("admin: administrators only")
	ErrInvalid        = errors.New("admin: invalid input")
	ErrConflict       = errors.New("admin: the project changed during export; export again")
	ErrExportRequired = errors.New("admin: export the project first; nothing may change after the export")
)

const (
	maxReasonChars = 500
	exportedAction = "project.exported"
	purgeReason    = "project purged after export"
	bundleName     = "repository.bundle"
)

// Config holds the Service's dependencies.
type Config struct {
	DB       *store.DB
	Audit    *audit.Recorder
	IDs      *id.Generator
	Clock    clock.Clock
	Runner   *gitrepo.Runner
	Projects *project.Service
	Files    *attachment.Store
	Collab   *collab.Service
}

// Service performs erasure, export and purge. It is safe for concurrent use.
type Service struct {
	cfg Config
}

// New returns an admin Service.
func New(cfg Config) *Service { return &Service{cfg: cfg} }

// EraseComment permanently removes a comment's text and attachment links.
// A thread root with replies stays as a placeholder so the replies keep
// their thread; anything else is deleted.
func (s *Service) EraseComment(ctx context.Context, actor auth.User, commentID, reason string) error {
	if err := s.check(actor, reason); err != nil {
		return err
	}
	return s.cfg.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		c, err := q.GetComment(ctx, commentID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := q.DeleteCommentAttachmentsOfComment(ctx, commentID); err != nil {
			return err
		}
		if err := q.DeleteCommentVersions(ctx, commentID); err != nil {
			return err
		}
		replies, err := q.CountReplies(ctx, sql.NullString{String: commentID, Valid: true})
		if err != nil {
			return err
		}
		if replies > 0 {
			err = q.MakeCommentPlaceholder(ctx, sqlcgen.MakeCommentPlaceholderParams{
				Now:   sql.NullInt64{Int64: s.cfg.Clock.Now().UnixMilli(), Valid: true},
				Actor: sql.NullString{String: actor.ID, Valid: true}, ID: commentID})
		} else {
			err = q.DeleteComment(ctx, commentID)
		}
		if err != nil {
			return err
		}
		if err := q.DeleteNotificationsAbout(ctx, commentID); err != nil {
			return err
		}
		return s.recordErasure(ctx, tx, q, actor, "comment", commentID, "", reason, c.ProjectID)
	})
}

// EraseAttachment permanently removes an attachment. Its file is removed
// once no other attachment of the project has the same content.
func (s *Service) EraseAttachment(ctx context.Context, actor auth.User, attachmentID, reason string) error {
	if err := s.check(actor, reason); err != nil {
		return err
	}
	var a sqlcgen.Attachment
	var others int64
	err := s.cfg.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		var err error
		a, err = q.GetAttachment(ctx, attachmentID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := q.DeleteCommentAttachmentsOfAttachment(ctx, attachmentID); err != nil {
			return err
		}
		if err := q.DeleteAttachment(ctx, attachmentID); err != nil {
			return err
		}
		if others, err = q.CountAttachmentsWithDigest(ctx, sqlcgen.CountAttachmentsWithDigestParams{ProjectID: a.ProjectID, Sha256: a.Sha256}); err != nil {
			return err
		}
		return s.recordErasure(ctx, tx, q, actor, "attachment", attachmentID, "", reason, a.ProjectID)
	})
	if err != nil || others > 0 {
		return err
	}
	var sum [sha256.Size]byte
	copy(sum[:], a.Sha256)
	return s.cfg.Files.Remove(a.ProjectID, sum)
}

// EraseDocument permanently removes text from a collaborative document.
// Yjs updates keep deleted text, so the document is rebuilt from
// cleanedText: every update and checkpoint is deleted and the generation
// increases, which loses per-character authorship (data-model.md,
// Retention). It returns the new generation.
func (s *Service) EraseDocument(ctx context.Context, actor auth.User, docID, cleanedText, reason string) (int64, error) {
	if err := s.check(actor, reason); err != nil {
		return 0, err
	}
	var projectID string
	err := s.cfg.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		d, err := sqlcgen.New(tx).GetCollabDocument(ctx, docID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		projectID = d.ProjectID
		return err
	})
	if err != nil {
		return 0, err
	}
	return s.cfg.Collab.Rebuild(ctx, docID, cleanedText, func(tx *sql.Tx) error {
		return s.recordErasure(ctx, tx, sqlcgen.New(tx), actor, "collab_document", docID, "", reason, projectID)
	})
}

// Export writes one zip archive of the project to w: a Git bundle with every
// ref, one JSON file per table with the project's rows, and the attachment
// files. It records project.exported with the archive's SHA-256, which it
// returns. If anything in the project changes while the archive is being
// written, the export fails with ErrConflict.
func (s *Service) Export(ctx context.Context, actor auth.User, projectName string, w io.Writer) (string, error) {
	if !actor.IsAdmin() {
		return "", ErrForbidden
	}
	p, dir, err := s.project(ctx, actor, projectName)
	if err != nil {
		return "", err
	}
	// Pushes wait while the archive is written, and the branch heads it
	// contains are recorded, so a purge can tell whether anything moved.
	unlock, err := s.cfg.Projects.LockRepo(ctx, projectName)
	if err != nil {
		return "", err
	}
	defer unlock()
	refsDigest, err := s.refsDigest(ctx, dir)
	if err != nil {
		return "", err
	}
	refs, err := s.cfg.Runner.AllRefs(ctx, dir)
	if err != nil {
		return "", err
	}
	// A project nobody pushed to has no refs, and git refuses an empty
	// bundle; its archive then has no repository.bundle.
	bundlePath := ""
	if len(refs) > 0 {
		bundle, err := os.CreateTemp(filepath.Dir(dir), ".export-*.bundle")
		if err != nil {
			return "", fmt.Errorf("admin: temporary bundle: %w", err)
		}
		bundlePath = bundle.Name()
		_ = bundle.Close()
		defer func() { _ = os.Remove(bundlePath) }()
		_ = os.Remove(bundlePath) // git bundle refuses to overwrite an existing file
		if err := s.cfg.Runner.Bundle(ctx, dir, bundlePath); err != nil {
			return "", err
		}
	}

	h := sha256.New()
	zw := zip.NewWriter(io.MultiWriter(w, h))
	if bundlePath != "" {
		if err := addFile(zw, bundleName, bundlePath); err != nil {
			return "", err
		}
	}
	var startEvent string
	var digests [][]byte
	err = s.cfg.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		if startEvent, err = sqlcgen.New(tx).LatestProjectEventID(ctx, sql.NullString{String: p.ID, Valid: true}); err != nil {
			return err
		}
		for _, t := range exportTables {
			if err := writeTable(ctx, tx, zw, t, p.ID); err != nil {
				return err
			}
		}
		digests, err = attachmentDigests(ctx, tx, p.ID)
		return err
	})
	if err != nil {
		return "", err
	}
	for _, d := range digests {
		name := hex.EncodeToString(d)
		if err := addFile(zw, "attachments/"+name, filepath.Join(s.attachmentsDir(), p.ID, name)); err != nil {
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		return "", fmt.Errorf("admin: finish archive: %w", err)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	err = s.cfg.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		latest, err := sqlcgen.New(tx).LatestProjectEventID(ctx, sql.NullString{String: p.ID, Valid: true})
		if err != nil {
			return err
		}
		if latest != startEvent {
			return ErrConflict
		}
		return s.cfg.Audit.Record(ctx, tx, audit.Event{ProjectID: p.ID, ActorID: actor.ID, Action: exportedAction,
			SubjectKind: "project", SubjectID: p.ID, Data: map[string]any{"sha256": sum, "refs": refsDigest}})
	})
	return sum, err
}

// Purge erases the project: every row, the repository and the attachment
// files, then compacts the database. typedName must repeat the project
// name, and the project's latest event must be its export, so nothing
// written after the export is lost.
func (s *Service) Purge(ctx context.Context, actor auth.User, projectName, typedName string) error {
	if !actor.IsAdmin() {
		return ErrForbidden
	}
	if typedName != projectName {
		return fmt.Errorf("%w: type the project name to confirm", ErrInvalid)
	}
	p, dir, err := s.project(ctx, actor, projectName)
	if err != nil {
		return err
	}
	unlock, err := s.cfg.Projects.LockRepo(ctx, projectName)
	if err != nil {
		return err
	}
	defer unlock()
	refsDigest, err := s.refsDigest(ctx, dir)
	if err != nil {
		return err
	}
	err = s.cfg.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		last, err := q.LatestProjectEventWithData(ctx, sql.NullString{String: p.ID, Valid: true})
		if errors.Is(err, sql.ErrNoRows) || err == nil && !exportCovers(last, refsDigest) {
			return ErrExportRequired
		}
		if err != nil {
			return err
		}
		for _, del := range purgeSteps(q) {
			if err := del(ctx, p.ID); err != nil {
				return err
			}
		}
		erasureID, err := s.cfg.IDs.New()
		if err != nil {
			return err
		}
		if err := q.InsertErasure(ctx, sqlcgen.InsertErasureParams{ID: erasureID, ActorID: actor.ID, Scope: "project",
			SubjectID: p.ID, SubjectLabel: p.Name, Reason: purgeReason, CreatedAt: s.cfg.Clock.Now().UnixMilli()}); err != nil {
			return err
		}
		return s.cfg.Audit.Record(ctx, tx, audit.Event{ActorID: actor.ID, Action: "project.purged",
			SubjectKind: "project", SubjectID: p.ID, Data: map[string]any{"name": p.Name}})
	})
	if err != nil {
		return err
	}
	// A crash from here on leaves directories without a project row; the
	// startup check removes them (data.md 6.6).
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("admin: remove repository: %w", err)
	}
	if err := s.cfg.Files.RemoveProject(p.ID); err != nil {
		return err
	}
	return s.cfg.DB.Compact(ctx)
}

func purgeSteps(q *sqlcgen.Queries) []func(context.Context, string) error {
	null := func(f func(context.Context, sql.NullString) error) func(context.Context, string) error {
		return func(ctx context.Context, id string) error { return f(ctx, sql.NullString{String: id, Valid: true}) }
	}
	return []func(context.Context, string) error{
		q.PurgeNotifications, q.PurgeCommentAttachments, q.PurgeCommentVersions, q.PurgeReplies, q.PurgeComments,
		q.PurgeCheckpoints, q.PurgeUpdates, q.PurgeVerdicts, q.PurgeStateChanges, q.PurgeRevisionCommits,
		q.PurgeRevisions, q.PurgeReviews, q.PurgeDiscussions, q.PurgeCollabDocuments, q.PurgeAttachments,
		null(q.PurgeAuditEvents), q.PurgeProject,
	}
}

func (s *Service) check(actor auth.User, reason string) error {
	if !actor.IsAdmin() {
		return ErrForbidden
	}
	if strings.TrimSpace(reason) == "" || !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > maxReasonChars {
		return fmt.Errorf("%w: a reason of 1-500 characters is required", ErrInvalid)
	}
	return nil
}

func (s *Service) recordErasure(ctx context.Context, tx *sql.Tx, q *sqlcgen.Queries, actor auth.User, scope, subjectID, label, reason, projectID string) error {
	erasureID, err := s.cfg.IDs.New()
	if err != nil {
		return err
	}
	if err := q.InsertErasure(ctx, sqlcgen.InsertErasureParams{ID: erasureID, ActorID: actor.ID, Scope: scope,
		SubjectID: subjectID, SubjectLabel: label, Reason: reason, CreatedAt: s.cfg.Clock.Now().UnixMilli()}); err != nil {
		return err
	}
	return s.cfg.Audit.Record(ctx, tx, audit.Event{ProjectID: projectID, ActorID: actor.ID, Action: scope + ".erased",
		SubjectKind: scope, SubjectID: subjectID})
}

func (s *Service) project(ctx context.Context, actor auth.User, name string) (project.Project, string, error) {
	p, err := s.cfg.Projects.Get(ctx, actor, name)
	if errors.Is(err, project.ErrNotFound) {
		return project.Project{}, "", ErrNotFound
	}
	if err != nil {
		return project.Project{}, "", err
	}
	dir, err := s.cfg.Projects.RepoPath(name)
	return p, dir, err
}

func (s *Service) attachmentsDir() string { return s.cfg.Files.Root() }

func addFile(zw *zip.Writer, name, path string) error {
	f, err := os.Open(path) // #nosec G304 -- paths are built from validated names and digests
	if err != nil {
		return fmt.Errorf("admin: open %s: %w", name, err)
	}
	defer func() { _ = f.Close() }()
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

// refsDigest is the SHA-256 of every ref of the repository (branches, tags,
// hidden refs: what the bundle holds), sorted, so any push changes it. A
// push to a branch without a review, or of a tag, writes no audit event,
// so the event order alone cannot tell that the export is stale.
func (s *Service) refsDigest(ctx context.Context, dir string) (string, error) {
	refs, err := s.cfg.Runner.AllRefs(ctx, dir)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(refs))
	for ref := range refs {
		names = append(names, ref)
	}
	slices.Sort(names)
	h := sha256.New()
	for _, ref := range names {
		_, _ = fmt.Fprintf(h, "%s %s\n", refs[ref], ref)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// exportCovers reports whether the project's latest event is an export
// taken with the branch heads it has now.
func exportCovers(last sqlcgen.LatestProjectEventWithDataRow, refsDigest string) bool {
	if last.Action != exportedAction {
		return false
	}
	var data struct {
		Refs string `json:"refs"`
	}
	return json.Unmarshal([]byte(last.Data), &data) == nil && data.Refs == refsDigest
}
