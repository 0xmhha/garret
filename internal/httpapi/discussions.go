package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/0xmhha/garret/internal/discussion"
)

type discussionJSON struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	AuthorID  string    `json:"author_id"`
	BodyDocID string    `json:"body_doc_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toDiscussionJSON(d discussion.Discussion) discussionJSON {
	return discussionJSON{ID: d.ID, Title: d.Title, AuthorID: d.AuthorID, BodyDocID: d.BodyDocID, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
}

func (a *API) listDiscussions(w http.ResponseWriter, r *http.Request) error {
	list, err := a.cfg.Discussions.List(r.Context(), user(r), r.PathValue("project"))
	if err != nil {
		return err
	}
	out := make([]discussionJSON, len(list))
	for i, d := range list {
		out[i] = toDiscussionJSON(d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"discussions": out})
	return nil
}

func (a *API) createDiscussion(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Title string `json:"title"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	d, err := a.cfg.Discussions.Create(r.Context(), user(r), r.PathValue("project"), in.Title)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toDiscussionJSON(d))
	return nil
}

func (a *API) getDiscussion(w http.ResponseWriter, r *http.Request) error {
	d, err := a.cfg.Discussions.Get(r.Context(), user(r), r.PathValue("project"), r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toDiscussionJSON(d))
	return nil
}

func (a *API) renameDiscussion(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Title string `json:"title"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if err := a.cfg.Discussions.Rename(r.Context(), user(r), r.PathValue("project"), r.PathValue("id"), in.Title); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) deleteDiscussion(w http.ResponseWriter, r *http.Request) error {
	if err := a.cfg.Discussions.Delete(r.Context(), user(r), r.PathValue("project"), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) discussionComments(w http.ResponseWriter, r *http.Request) error {
	list, err := a.cfg.Comments.ListForDiscussion(r.Context(), user(r), r.PathValue("project"), r.PathValue("id"))
	if err != nil {
		return err
	}
	out := make([]commentJSON, len(list))
	for i, c := range list {
		out[i] = toCommentJSON(c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"comments": out})
	return nil
}

func (a *API) createDiscussionComment(w http.ResponseWriter, r *http.Request) error {
	var in newCommentJSON
	if err := decode(w, r, &in); err != nil {
		return err
	}
	nc := in.toNewComment()
	nc.Discussion = r.PathValue("id")
	c, err := a.cfg.Comments.Create(r.Context(), user(r), r.PathValue("project"), nc)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toCommentJSON(c))
	return nil
}

func (a *API) discussionHistory(w http.ResponseWriter, r *http.Request) error {
	limit, err := pageLimit(r)
	if err != nil {
		return err
	}
	var after int64
	if c := r.URL.Query().Get("cursor"); c != "" {
		if after, err = strconv.ParseInt(c, 10, 64); err != nil || after < 0 {
			return fmt.Errorf("%w: cursor", errBadRequest)
		}
	}
	list, next, err := a.cfg.Discussions.History(r.Context(), user(r), r.PathValue("project"), r.PathValue("id"), after, limit)
	if err != nil {
		return err
	}
	type checkpointJSON struct {
		UpToSeq      int64     `json:"up_to_seq"`
		Text         string    `json:"text"`
		Contributors []string  `json:"contributors"`
		At           time.Time `json:"at"`
	}
	out := make([]checkpointJSON, len(list))
	for i, c := range list {
		out[i] = checkpointJSON{UpToSeq: c.UpToSeq, Text: c.Text, Contributors: c.Contributors, At: c.At}
	}
	cursor := ""
	if next > 0 {
		cursor = strconv.FormatInt(next, 10)
	}
	writeJSON(w, http.StatusOK, map[string]any{"checkpoints": out, "next_cursor": cursor})
	return nil
}
