package httpapi

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/0xmhha/garret/internal/comment"
	"github.com/0xmhha/garret/internal/gitrepo"
	"github.com/0xmhha/garret/internal/project"
	"github.com/0xmhha/garret/internal/review"
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

type projectJSON struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	CloneURL    string    `json:"clone_url"`
}

func (a *API) toProjectJSON(p project.Project) projectJSON {
	return projectJSON{ID: p.ID, Name: p.Name, Description: p.Description, CreatedAt: p.CreatedAt,
		CloneURL: a.cfg.Origin + "/git/" + p.Name + ".git"}
}

func (a *API) listProjects(w http.ResponseWriter, r *http.Request) error {
	list, err := a.cfg.Projects.List(r.Context(), user(r))
	if err != nil {
		return err
	}
	out := make([]projectJSON, len(list))
	for i, p := range list {
		out[i] = a.toProjectJSON(p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
	return nil
}

func (a *API) createProject(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	p, err := a.cfg.Projects.Create(r.Context(), user(r), in.Name, in.Description)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, a.toProjectJSON(p))
	return nil
}

func (a *API) getProject(w http.ResponseWriter, r *http.Request) error {
	p, err := a.cfg.Projects.Get(r.Context(), user(r), r.PathValue("project"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, a.toProjectJSON(p))
	return nil
}

func (a *API) branches(w http.ResponseWriter, r *http.Request) error {
	refs, err := a.cfg.Projects.Branches(r.Context(), user(r), r.PathValue("project"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"branches": refs})
	return nil
}

type reviewJSON struct {
	ID            string       `json:"id"`
	Number        int64        `json:"number"`
	Title         string       `json:"title"`
	SourceBranch  string       `json:"source_branch"`
	TargetBranch  string       `json:"target_branch"`
	State         review.State `json:"state"`
	AuthorID      string       `json:"author_id"`
	DescriptionID string       `json:"description_doc_id"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

func toReviewJSON(rv review.Review) reviewJSON {
	return reviewJSON{ID: rv.ID, Number: rv.Number, Title: rv.Title, SourceBranch: rv.SourceBranch, TargetBranch: rv.TargetBranch,
		State: rv.State, AuthorID: rv.AuthorID, DescriptionID: rv.DescriptionID, CreatedAt: rv.CreatedAt, UpdatedAt: rv.UpdatedAt}
}

func number(r *http.Request) (int64, error) {
	n, err := strconv.ParseInt(r.PathValue("n"), 10, 64)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%w: review number", errBadRequest)
	}
	return n, nil
}

func (a *API) listReviews(w http.ResponseWriter, r *http.Request) error {
	list, err := a.cfg.Reviews.List(r.Context(), user(r), r.PathValue("project"))
	if err != nil {
		return err
	}
	state := review.State(r.URL.Query().Get("state"))
	author := r.URL.Query().Get("author")
	out := []reviewJSON{}
	for _, rv := range list {
		if (state == "" || rv.State == state) && (author == "" || rv.AuthorID == author) {
			out = append(out, toReviewJSON(rv))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"reviews": out})
	return nil
}

func (a *API) openReview(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		Title        string `json:"title"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	rv, err := a.cfg.Reviews.Open(r.Context(), user(r), r.PathValue("project"), in.SourceBranch, in.TargetBranch, in.Title)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toReviewJSON(rv))
	return nil
}

func (a *API) getReview(w http.ResponseWriter, r *http.Request) error {
	n, err := number(r)
	if err != nil {
		return err
	}
	d, err := a.cfg.Reviews.Get(r.Context(), user(r), r.PathValue("project"), n)
	if err != nil {
		return err
	}
	type commitJSON struct {
		SHA        string    `json:"sha"`
		Subject    string    `json:"subject"`
		AuthorName string    `json:"author_name"`
		AuthoredAt time.Time `json:"authored_at"`
	}
	type revisionJSON struct {
		Number         int64        `json:"number"`
		BaseSHA        string       `json:"base_sha"`
		HeadSHA        string       `json:"head_sha"`
		CommitsOmitted bool         `json:"commits_omitted"`
		PushedBy       string       `json:"pushed_by"`
		PushedAt       time.Time    `json:"pushed_at"`
		Commits        []commitJSON `json:"commits"`
	}
	type verdictJSON struct {
		UserID string         `json:"user_id"`
		Value  review.Verdict `json:"value"`
		At     time.Time      `json:"at"`
	}
	revs := make([]revisionJSON, len(d.Revisions))
	for i, rv := range d.Revisions {
		revs[i] = revisionJSON{Number: rv.Number, BaseSHA: rv.BaseSHA, HeadSHA: rv.HeadSHA, CommitsOmitted: rv.CommitsOmitted,
			PushedBy: rv.PushedBy, PushedAt: rv.PushedAt, Commits: []commitJSON{}}
		for _, c := range rv.Commits {
			revs[i].Commits = append(revs[i].Commits, commitJSON{SHA: c.SHA, Subject: c.Subject, AuthorName: c.AuthorName,
				AuthoredAt: time.Unix(c.AuthoredAt, 0).UTC()})
		}
	}
	verdicts := make([]verdictJSON, len(d.Verdicts))
	for i, v := range d.Verdicts {
		verdicts[i] = verdictJSON{UserID: v.UserID, Value: v.Value, At: v.At}
	}
	writeJSON(w, http.StatusOK, map[string]any{"review": toReviewJSON(d.Review), "revisions": revs, "verdicts": verdicts})
	return nil
}

func (a *API) changeState(w http.ResponseWriter, r *http.Request) error {
	n, err := number(r)
	if err != nil {
		return err
	}
	var in struct {
		From review.State `json:"from"`
		To   review.State `json:"to"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if err := a.cfg.Reviews.ChangeState(r.Context(), user(r), r.PathValue("project"), n, in.From, in.To); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) recordVerdict(w http.ResponseWriter, r *http.Request) error {
	n, err := number(r)
	if err != nil {
		return err
	}
	var in struct {
		Value review.Verdict `json:"value"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if err := a.cfg.Reviews.RecordVerdict(r.Context(), user(r), r.PathValue("project"), n, in.Value); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// diff answers ?rev=N, ?commit=SHA, ?from=N&to=N (revision pair) or
// ?from=SHA&to=SHA (commit range).
func (a *API) diff(w http.ResponseWriter, r *http.Request) error {
	n, err := number(r)
	if err != nil {
		return err
	}
	q, err := diffQuery(r)
	if err != nil {
		return err
	}
	s, err := a.cfg.Reviews.Diff(r.Context(), user(r), r.PathValue("project"), n, q)
	if err != nil {
		return err
	}
	type fileJSON struct {
		Status     string `json:"status"`
		OldPath    string `json:"old_path"`
		NewPath    string `json:"new_path"`
		Added      int    `json:"added"`
		Deleted    int    `json:"deleted"`
		Binary     bool   `json:"binary"`
		Similarity int    `json:"similarity"`
	}
	files := make([]fileJSON, len(s.Files))
	for i, f := range s.Files {
		files[i] = fileJSON{Status: string(rune(f.Status)), OldPath: f.OldPath, NewPath: f.NewPath, Added: f.Added,
			Deleted: f.Deleted, Binary: f.Binary, Similarity: f.Similarity}
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": s.From, "to": s.To, "bases_differ": s.BasesDiffer, "files": files})
	return nil
}

func diffQuery(r *http.Request) (review.DiffQuery, error) {
	v := r.URL.Query()
	parseNo := func(s string) (int64, bool) {
		n, err := strconv.ParseInt(s, 10, 64)
		return n, err == nil && n > 0
	}
	switch {
	case v.Get("rev") != "":
		n, ok := parseNo(v.Get("rev"))
		if !ok {
			return review.DiffQuery{}, fmt.Errorf("%w: rev", errBadRequest)
		}
		return review.DiffQuery{Revision: n}, nil
	case v.Get("commit") != "":
		if !commitPattern.MatchString(v.Get("commit")) {
			return review.DiffQuery{}, fmt.Errorf("%w: commit", errBadRequest)
		}
		return review.DiffQuery{Commit: v.Get("commit")}, nil
	case v.Get("from") != "" && v.Get("to") != "":
		a, okA := parseNo(v.Get("from"))
		b, okB := parseNo(v.Get("to"))
		if okA && okB {
			return review.DiffQuery{FromRevision: a, ToRevision: b}, nil
		}
		if commitPattern.MatchString(v.Get("from")) && commitPattern.MatchString(v.Get("to")) {
			return review.DiffQuery{From: v.Get("from"), To: v.Get("to")}, nil
		}
	}
	return review.DiffQuery{}, fmt.Errorf("%w: choose rev, commit, or from and to", errBadRequest)
}

// fileDiff answers ?from=SHA&to=SHA&old=PATH&new=PATH with the file's hunks.
// A file over the limits answers 413 so the page can show it as too large.
func (a *API) fileDiff(w http.ResponseWriter, r *http.Request) error {
	n, err := number(r)
	if err != nil {
		return err
	}
	v := r.URL.Query()
	if !commitPattern.MatchString(v.Get("from")) || !commitPattern.MatchString(v.Get("to")) {
		return fmt.Errorf("%w: from and to must be commits", errBadRequest)
	}
	hunks, err := a.cfg.Reviews.FileDiff(r.Context(), user(r), r.PathValue("project"), n, v.Get("from"), v.Get("to"), v.Get("old"), v.Get("new"))
	if err != nil {
		return err
	}
	type lineJSON struct {
		Kind    string `json:"kind"`
		Text    string `json:"text"`
		Old     int    `json:"old,omitempty"`
		New     int    `json:"new,omitempty"`
		NoEOLNL bool   `json:"no_newline,omitempty"`
	}
	type hunkJSON struct {
		OldStart int        `json:"old_start"`
		OldLines int        `json:"old_lines"`
		NewStart int        `json:"new_start"`
		NewLines int        `json:"new_lines"`
		Section  string     `json:"section"`
		Lines    []lineJSON `json:"lines"`
	}
	kinds := map[gitrepo.LineKind]string{gitrepo.LineContext: "context", gitrepo.LineAdded: "added", gitrepo.LineDeleted: "deleted"}
	out := make([]hunkJSON, len(hunks))
	for i, h := range hunks {
		out[i] = hunkJSON{OldStart: h.OldStart, OldLines: h.OldLines, NewStart: h.NewStart, NewLines: h.NewLines, Section: h.Section}
		for _, l := range h.Lines {
			out[i].Lines = append(out[i].Lines, lineJSON{Kind: kinds[l.Kind], Text: string(l.Text), Old: l.OldNo, New: l.NewNo, NoEOLNL: l.NoNewlineAtEOF})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"hunks": out})
	return nil
}

type anchorJSON struct {
	Revision int64  `json:"revision"`
	View     string `json:"view"`
	From     string `json:"from"`
	To       string `json:"to"`
	Path     string `json:"path"`
	Side     string `json:"side"`
	Start    int    `json:"start_line"`
	End      int    `json:"end_line"`
}

type commentJSON struct {
	ID           string      `json:"id"`
	ThreadRootID string      `json:"thread_root_id"`
	AuthorID     string      `json:"author_id"`
	Anchor       *anchorJSON `json:"anchor"`
	Ported       *portedJSON `json:"ported,omitempty"`
	Resolved     bool        `json:"resolved"`
	Version      int64       `json:"version"`
	Body         string      `json:"body"`
	Deleted      bool        `json:"deleted"`
	Attachments  []string    `json:"attachments"`
	CreatedAt    time.Time   `json:"created_at"`
}

type portedJSON struct {
	Path      string `json:"path"`
	Side      string `json:"side"`
	Start     int    `json:"start_line"`
	End       int    `json:"end_line"`
	FileLevel bool   `json:"file_level"`
	Dropped   bool   `json:"dropped"`
}

func toCommentJSON(c comment.Comment) commentJSON {
	out := commentJSON{ID: c.ID, ThreadRootID: c.ThreadRootID, AuthorID: c.AuthorID, Resolved: c.Resolved, Version: c.Version,
		Body: c.Body, Deleted: c.Deleted, Attachments: c.Attachments, CreatedAt: c.CreatedAt}
	if out.Attachments == nil {
		out.Attachments = []string{}
	}
	if c.Anchor != nil {
		out.Anchor = &anchorJSON{Revision: c.Anchor.Revision, View: string(c.Anchor.View), From: c.Anchor.From, To: c.Anchor.To,
			Path: c.Anchor.Path, Side: string(c.Anchor.Side), Start: c.Anchor.Start, End: c.Anchor.End}
	}
	return out
}

// reviewComments lists a review's comments. With ?rev=N, anchored comments
// written on earlier revisions also carry their ported position.
func (a *API) reviewComments(w http.ResponseWriter, r *http.Request) error {
	n, err := number(r)
	if err != nil {
		return err
	}
	project := r.PathValue("project")
	list, err := a.cfg.Comments.ListForReview(r.Context(), user(r), project, n)
	if err != nil {
		return err
	}
	out := make([]commentJSON, len(list))
	for i, c := range list {
		out[i] = toCommentJSON(c)
	}
	if revParam := r.URL.Query().Get("rev"); revParam != "" {
		rev, err := strconv.ParseInt(revParam, 10, 64)
		if err != nil || rev < 1 {
			return fmt.Errorf("%w: rev", errBadRequest)
		}
		ported, err := a.cfg.Reviews.PortAnchors(r.Context(), user(r), project, n, rev)
		if err != nil {
			return err
		}
		byID := map[string]review.PortedAnchor{}
		for _, p := range ported {
			byID[p.CommentID] = p
		}
		for i := range out {
			p, ok := byID[out[i].ID]
			if !ok {
				continue
			}
			if out[i].Anchor != nil {
				out[i].Anchor.Revision = p.AnchorRevisionNo
			}
			if p.Position != nil {
				out[i].Ported = &portedJSON{Path: p.Position.Path, Side: p.Position.Side, Start: p.Position.Start,
					End: p.Position.End, FileLevel: p.Position.FileLevel, Dropped: p.Position.Dropped}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"comments": out})
	return nil
}

type newCommentJSON struct {
	ReplyTo     string      `json:"reply_to"`
	Anchor      *anchorJSON `json:"anchor"`
	Body        string      `json:"body"`
	Attachments []string    `json:"attachments"`
}

func (in newCommentJSON) toNewComment() comment.NewComment {
	nc := comment.NewComment{ReplyTo: in.ReplyTo, Body: in.Body, Attachments: in.Attachments}
	if a := in.Anchor; a != nil {
		nc.Anchor = &comment.Anchor{Revision: a.Revision, View: comment.View(a.View), From: a.From, To: a.To, Path: a.Path,
			Side: comment.Side(a.Side), Start: a.Start, End: a.End}
	}
	return nc
}

func (a *API) createReviewComment(w http.ResponseWriter, r *http.Request) error {
	n, err := number(r)
	if err != nil {
		return err
	}
	var in newCommentJSON
	if err := decode(w, r, &in); err != nil {
		return err
	}
	nc := in.toNewComment()
	nc.Review = n
	c, err := a.cfg.Comments.Create(r.Context(), user(r), r.PathValue("project"), nc)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toCommentJSON(c))
	return nil
}

func (a *API) editComment(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Version int64  `json:"version"`
		Body    string `json:"body"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	c, err := a.cfg.Comments.Edit(r.Context(), user(r), r.PathValue("id"), in.Version, in.Body)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toCommentJSON(c))
	return nil
}

func (a *API) resolveComment(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Resolved *bool `json:"resolved"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if in.Resolved == nil {
		return fmt.Errorf("%w: resolved is required", errBadRequest)
	}
	if err := a.cfg.Comments.Resolve(r.Context(), user(r), r.PathValue("id"), *in.Resolved); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) deleteComment(w http.ResponseWriter, r *http.Request) error {
	if err := a.cfg.Comments.Delete(r.Context(), user(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) commentVersions(w http.ResponseWriter, r *http.Request) error {
	versions, err := a.cfg.Comments.Versions(r.Context(), user(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		return comment.ErrNotFound
	}
	type versionJSON struct {
		Version   int64     `json:"version"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
	}
	out := make([]versionJSON, len(versions))
	for i, v := range versions {
		out[i] = versionJSON{Version: v.Number, Body: v.Body, CreatedAt: v.CreatedAt}
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": out})
	return nil
}
