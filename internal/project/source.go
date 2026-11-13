package project

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/0xmhha/garret/internal/audit"
	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/gitrepo"
)

// ErrNoSource reports a sync of a project created without a source path.
var ErrNoSource = errors.New("project: the project has no source repository")

const branchPrefix = "refs/heads/"

// BranchMove is a branch head that an import changed, in the shape of a
// push: Old is "" for a new branch.
type BranchMove struct {
	Ref, Old, New string
}

// SyncResult reports what an import did, by branch name (without
// refs/heads/), each list sorted. Moves are the changes to hand to review
// processing as a push by the caller.
type SyncResult struct {
	Created   []string
	Updated   []string
	Rewritten []string // left alone: not a fast-forward, and no overwrite
	Unchanged []string
	Moves     []BranchMove
}

// Preview returns the branch names of the Git repository at path on the
// server's machine, sorted, before a project is created from it. Only
// administrators may look.
func (s *Service) Preview(ctx context.Context, actor auth.User, path string) ([]string, error) {
	if !actor.IsAdmin() {
		return nil, ErrForbidden
	}
	branches, err := s.sourceBranches(ctx, path)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(branches))
	for ref := range branches {
		names = append(names, strings.TrimPrefix(ref, branchPrefix))
	}
	slices.Sort(names)
	return names, nil
}

// CreateFromSource creates a project whose repository starts with every
// branch of the repository at source, and remembers source for Sync
// (decisions.md D15). Only administrators may create projects.
func (s *Service) CreateFromSource(ctx context.Context, actor auth.User, name, description, source string) (Project, error) {
	if !actor.IsAdmin() {
		return Project{}, ErrForbidden
	}
	if _, err := s.sourceBranches(ctx, source); err != nil {
		return Project{}, err
	}
	return s.create(ctx, actor, name, description, source)
}

// Sync copies the project's source branches again: new branches and
// fast-forwards always, branches the source rewrote only with overwrite.
// Branches missing from the source are kept. Any member may sync; the
// caller records the moves as its push.
func (s *Service) Sync(ctx context.Context, actor auth.User, name string, overwrite bool) (SyncResult, error) {
	p, err := s.Get(ctx, actor, name)
	if err != nil {
		return SyncResult{}, err
	}
	if p.SourcePath == "" {
		return SyncResult{}, ErrNoSource
	}
	dir, err := s.RepoPath(name)
	if err != nil {
		return SyncResult{}, err
	}
	unlock, err := s.repos.Lock(ctx, name)
	if err != nil {
		return SyncResult{}, err
	}
	defer unlock()
	res, err := s.importBranches(ctx, dir, p.SourcePath, overwrite)
	if err != nil {
		return SyncResult{}, err
	}
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		return s.audit.Record(ctx, tx, audit.Event{ProjectID: p.ID, ActorID: actor.ID, Action: "project.synced",
			SubjectKind: "project", SubjectID: p.ID, Data: map[string]any{"created": res.Created, "updated": res.Updated,
				"rewritten": res.Rewritten, "overwrite": overwrite}})
	})
	return res, err
}

// sourceBranches lists a source's branches, mapping a path that is not a
// repository to ErrInvalid.
func (s *Service) sourceBranches(ctx context.Context, path string) (map[string]string, error) {
	branches, err := s.runner.SourceBranches(ctx, path)
	if errors.Is(err, gitrepo.ErrBadArgument) || errors.Is(err, gitrepo.ErrNotRepository) {
		return nil, fmt.Errorf("%w: %s is not an absolute path to a Git repository", ErrInvalid, path)
	}
	return branches, err
}

// importBranches stages the source's branches under the import prefix,
// moves the project's branches, and removes the staging refs. The caller
// holds the repository lock or owns a repository nobody else sees yet.
func (s *Service) importBranches(ctx context.Context, dir, source string, overwrite bool) (SyncResult, error) {
	if err := s.clearStaging(ctx, dir); err != nil {
		return SyncResult{}, err
	}
	branches, err := s.sourceBranches(ctx, source)
	if err != nil {
		return SyncResult{}, err
	}
	refs := make([]string, 0, len(branches))
	for ref := range branches {
		refs = append(refs, ref)
	}
	slices.Sort(refs)
	if err := s.runner.FetchBranches(ctx, dir, source, refs); err != nil {
		return SyncResult{}, err
	}
	defer func() { _ = s.clearStaging(context.WithoutCancel(ctx), dir) }()
	current, err := s.runner.Refs(ctx, dir)
	if err != nil {
		return SyncResult{}, err
	}
	staged, err := s.runner.AllRefs(ctx, dir)
	if err != nil {
		return SyncResult{}, err
	}
	var res SyncResult
	for _, ref := range refs {
		name := strings.TrimPrefix(ref, branchPrefix)
		next, old := staged[gitrepo.ImportRefPrefix+name], current[ref]
		if next == "" {
			continue // the branch vanished between listing and fetching
		}
		switch old {
		case next:
			res.Unchanged = append(res.Unchanged, name)
			continue
		case "":
			res.Created = append(res.Created, name)
		default:
			ff, err := s.runner.IsAncestor(ctx, dir, old, next)
			if err != nil {
				return SyncResult{}, err
			}
			if !ff && !overwrite {
				res.Rewritten = append(res.Rewritten, name)
				continue
			}
			res.Updated = append(res.Updated, name)
		}
		if err := s.runner.SetBranch(ctx, dir, ref, next, old); err != nil {
			return SyncResult{}, err
		}
		res.Moves = append(res.Moves, BranchMove{Ref: ref, Old: old, New: next})
	}
	return res, nil
}

// clearStaging removes import refs a previous, interrupted sync left.
func (s *Service) clearStaging(ctx context.Context, dir string) error {
	all, err := s.runner.AllRefs(ctx, dir)
	if err != nil {
		return err
	}
	for ref, sha := range all {
		if strings.HasPrefix(ref, gitrepo.ImportRefPrefix) {
			if err := s.runner.DeleteImportRef(ctx, dir, ref, sha); err != nil {
				return err
			}
		}
	}
	return nil
}
