package project

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/clock"
	"github.com/0xmhha/garret/internal/gitrepo"
	"github.com/0xmhha/garret/internal/id"
	"github.com/0xmhha/garret/internal/store"
	"github.com/0xmhha/garret/internal/store/sqlcgen"
)

// Service errors.
var (
	ErrNotFound  = errors.New("project: not found")
	ErrForbidden = errors.New("project: not allowed")
	ErrInvalid   = errors.New("project: invalid input")
	ErrConflict  = errors.New("project: already exists")
)

const (
	maxDescriptionChars = 500
	repoSuffix          = ".git"
	repoMode            = 0o700
)

// Project is a project as services see it.
type Project struct {
	ID          string
	Name        string
	Description string
	CreatedBy   string
	CreatedAt   time.Time
}

// Service manages projects and their repositories. It is safe for
// concurrent use.
type Service struct {
	db       *store.DB
	audit    *audit.Recorder
	ids      *id.Generator
	clock    clock.Clock
	runner   *gitrepo.Runner
	reposDir string
	names    store.KeyedMutex
}

// New returns a Service that keeps repositories in reposDir.
func New(db *store.DB, rec *audit.Recorder, ids *id.Generator, c clock.Clock, runner *gitrepo.Runner, reposDir string) *Service {
	return &Service{db: db, audit: rec, ids: ids, clock: c, runner: runner, reposDir: reposDir}
}

// RepoPath returns the bare repository directory of a valid project name.
func (s *Service) RepoPath(name string) (string, error) {
	if err := gitrepo.ValidProjectName(name); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return filepath.Join(s.reposDir, name+repoSuffix), nil
}

// Create creates a project and its bare repository. Only administrators may
// create projects. The repository directory is created exclusively first,
// so a concurrent or repeated create never touches an existing repository.
func (s *Service) Create(ctx context.Context, actor auth.User, name, description string) (Project, error) {
	if !actor.IsAdmin() {
		return Project{}, ErrForbidden
	}
	dir, err := s.RepoPath(name)
	if err != nil {
		return Project{}, err
	}
	if !utf8.ValidString(description) || utf8.RuneCountInString(description) > maxDescriptionChars {
		return Project{}, fmt.Errorf("%w: description must be at most 500 characters", ErrInvalid)
	}
	unlock, err := s.names.Lock(ctx, name)
	if err != nil {
		return Project{}, err
	}
	defer unlock()
	if err := os.Mkdir(dir, repoMode); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Project{}, ErrConflict
		}
		return Project{}, fmt.Errorf("project: create repository directory: %w", err)
	}
	created := false
	defer func() {
		if !created {
			_ = os.RemoveAll(dir)
		}
	}()
	if err := s.runner.InitBare(ctx, dir); err != nil {
		return Project{}, err
	}
	projectID, err := s.ids.New()
	if err != nil {
		return Project{}, err
	}
	now := s.clock.Now()
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		err := sqlcgen.New(tx).InsertProject(ctx, sqlcgen.InsertProjectParams{
			ID: projectID, Name: name, Description: description, CreatedBy: actor.ID, CreatedAt: now.UnixMilli()})
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed: projects.name") {
				return ErrConflict
			}
			return err
		}
		return s.audit.Record(ctx, tx, audit.Event{ProjectID: projectID, ActorID: actor.ID, Action: "project.created",
			SubjectKind: "project", SubjectID: projectID, Data: map[string]any{"name": name}})
	})
	if err != nil {
		return Project{}, err
	}
	created = true
	return Project{ID: projectID, Name: name, Description: description, CreatedBy: actor.ID,
		CreatedAt: time.UnixMilli(now.UnixMilli()).UTC()}, nil
}

// Get returns the project named name. Every member may see every project.
func (s *Service) Get(ctx context.Context, _ auth.User, name string) (Project, error) {
	var p sqlcgen.Project
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		p, err = sqlcgen.New(tx).GetProjectByName(ctx, name)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return toProject(p), err
}

// List returns every project by name.
func (s *Service) List(ctx context.Context, _ auth.User) ([]Project, error) {
	var rows []sqlcgen.Project
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		rows, err = sqlcgen.New(tx).ListProjects(ctx)
		return err
	})
	out := make([]Project, len(rows))
	for i, p := range rows {
		out[i] = toProject(p)
	}
	return out, err
}

// Branches returns the project's branch heads by full ref name.
func (s *Service) Branches(ctx context.Context, actor auth.User, name string) (map[string]string, error) {
	if _, err := s.Get(ctx, actor, name); err != nil {
		return nil, err
	}
	dir, err := s.RepoPath(name)
	if err != nil {
		return nil, err
	}
	return s.runner.Refs(ctx, dir)
}

// RemoveOrphanRepositories removes repository directories that have no
// project row, left behind by a crash during create or purge. It runs at
// startup before any request is served and returns the removed names.
func (s *Service) RemoveOrphanRepositories(ctx context.Context) ([]string, error) {
	var names []string
	err := s.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		names, err = sqlcgen.New(tx).ListProjectNames(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(names))
	for _, n := range names {
		known[n+repoSuffix] = true
	}
	entries, err := os.ReadDir(s.reposDir)
	if err != nil {
		return nil, fmt.Errorf("project: list repositories: %w", err)
	}
	var removed []string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), repoSuffix) || known[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.reposDir, e.Name())); err != nil {
			return removed, fmt.Errorf("project: remove orphan %s: %w", e.Name(), err)
		}
		removed = append(removed, e.Name())
	}
	return removed, nil
}

func toProject(p sqlcgen.Project) Project {
	return Project{ID: p.ID, Name: p.Name, Description: p.Description, CreatedBy: p.CreatedBy,
		CreatedAt: time.UnixMilli(p.CreatedAt).UTC()}
}
