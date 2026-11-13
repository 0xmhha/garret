package httpapi

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/0xmhha/garret/internal/attachment"
	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/inbox"
)

// uploadOverhead allows for the multipart framing around a file at the
// size limit.
const uploadOverhead = 1 << 20

type attachmentJSON struct {
	ID        string    `json:"id"`
	Filename  string    `json:"filename"`
	MediaType string    `json:"media_type"`
	Size      int64     `json:"size"`
	Inline    bool      `json:"inline"`
	CreatedAt time.Time `json:"created_at"`
}

func toAttachmentJSON(a attachment.Attachment) attachmentJSON {
	return attachmentJSON{ID: a.ID, Filename: a.Filename, MediaType: a.MediaType, Size: a.Size, Inline: a.Inline, CreatedAt: a.CreatedAt}
}

// upload streams the multipart part named "file" into the attachment store
// without buffering it in memory or on disk elsewhere.
func (a *API) upload(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, attachment.DefaultMaxSize+uploadOverhead)
	mr, err := r.MultipartReader()
	if err != nil {
		return fmt.Errorf("%w: expected multipart/form-data", errBadRequest)
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%w: no file part", errBadRequest)
		}
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errBodyTooLarge
		}
		if err != nil {
			return fmt.Errorf("%w: %w", errBadRequest, err)
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		att, err := a.cfg.Attachments.Upload(r.Context(), user(r), r.PathValue("project"), part.FileName(), part)
		_ = part.Close()
		if err != nil {
			if errors.As(err, &tooLarge) {
				return errBodyTooLarge
			}
			return err
		}
		writeJSON(w, http.StatusCreated, toAttachmentJSON(att))
		return nil
	}
}

// download serves an attachment as a download, or inline for the image
// types attachment.Inline allows. The page's CSP also sandboxes it.
func (a *API) download(w http.ResponseWriter, r *http.Request) error {
	att, f, err := a.cfg.Attachments.Open(r.Context(), user(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; sandbox")
	disposition := "attachment"
	contentType := "application/octet-stream"
	if att.Inline {
		disposition, contentType = "inline", att.MediaType
	}
	h.Set("Content-Type", contentType)
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": att.Filename}))
	h.Set("Content-Length", strconv.FormatInt(att.Size, 10))
	w.WriteHeader(http.StatusOK)
	_, err = io.Copy(w, f)
	if err != nil {
		a.cfg.Log.Warn("attachment download interrupted", "attachment", att.ID, "err", err)
	}
	return nil
}

// attachmentMeta describes an attachment without sending its file, so the
// interface can name and size it (api.md, Attachments).
func (a *API) attachmentMeta(w http.ResponseWriter, r *http.Request) error {
	att, err := a.cfg.Attachments.Info(r.Context(), user(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toAttachmentJSON(att))
	return nil
}

func (a *API) deleteAttachment(w http.ResponseWriter, r *http.Request) error {
	if err := a.cfg.Attachments.Delete(r.Context(), user(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) listInbox(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := a.cfg.Inbox.List(r.Context(), user(r), q.Get("unread") == "true", q.Get("cursor"), limit)
	if err != nil {
		return err
	}
	type noteJSON struct {
		ID          string            `json:"id"`
		ProjectID   string            `json:"project_id"`
		Kind        inbox.Kind        `json:"kind"`
		SubjectKind inbox.SubjectKind `json:"subject_kind"`
		SubjectID   string            `json:"subject_id"`
		CreatedAt   time.Time         `json:"created_at"`
		ReadAt      *time.Time        `json:"read_at"`
		Project     string            `json:"project"`
		Review      int64             `json:"review,omitempty"`
		Discussion  string            `json:"discussion,omitempty"`
	}
	out := make([]noteJSON, len(list))
	next := ""
	for i, n := range list {
		out[i] = noteJSON{ID: n.ID, ProjectID: n.ProjectID, Kind: n.Kind, SubjectKind: n.SubjectKind, SubjectID: n.SubjectID,
			Project: n.Project, Review: n.Review, Discussion: n.Discussion,
			CreatedAt: n.CreatedAt, ReadAt: n.ReadAt}
		next = n.ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": out, "next_cursor": next})
	return nil
}

func (a *API) markRead(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		IDs []string `json:"ids"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if err := a.cfg.Inbox.MarkRead(r.Context(), user(r), in.IDs); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) error {
	users, err := a.cfg.Auth.ListUsers(r.Context(), user(r))
	if err != nil {
		return err
	}
	out := make([]userJSON, len(users))
	for i, u := range users {
		out[i] = toUserJSON(u)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
	return nil
}

func (a *API) adminListUsers(w http.ResponseWriter, r *http.Request) error {
	if !user(r).IsAdmin() {
		return auth.ErrForbidden
	}
	return a.listUsers(w, r)
}

func (a *API) disableUser(w http.ResponseWriter, r *http.Request) error {
	if err := a.cfg.Auth.DisableUser(r.Context(), user(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type invitationJSON struct {
	ID        string     `json:"id"`
	Role      auth.Role  `json:"role"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at"`
	UsedAt    *time.Time `json:"used_at"`
	UsedBy    string     `json:"used_by"`
}

func toInvitationJSON(i auth.Invitation) invitationJSON {
	return invitationJSON{ID: i.ID, Role: i.Role, CreatedAt: i.CreatedAt, ExpiresAt: i.ExpiresAt, RevokedAt: i.RevokedAt, UsedAt: i.UsedAt, UsedBy: i.UsedBy}
}

func (a *API) listInvitations(w http.ResponseWriter, r *http.Request) error {
	list, err := a.cfg.Auth.ListInvitations(r.Context(), user(r))
	if err != nil {
		return err
	}
	out := make([]invitationJSON, len(list))
	for i, inv := range list {
		out[i] = toInvitationJSON(inv)
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": out})
	return nil
}

// createInvitation returns the secret-free link and, separately, the code
// the administrator gives the member by another channel (transport.md 6.1).
func (a *API) createInvitation(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Role auth.Role `json:"role"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	inv, code, err := a.cfg.Auth.CreateInvitation(r.Context(), user(r), in.Role)
	if err != nil {
		return err
	}
	if a.cfg.OnInvitationsChanged != nil {
		a.cfg.OnInvitationsChanged()
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"invitation": toInvitationJSON(inv),
		"link":       a.cfg.EnrollURL + "/enroll#ca=" + a.cfg.CAFingerprint,
		"code":       code,
	})
	return nil
}

func (a *API) revokeInvitation(w http.ResponseWriter, r *http.Request) error {
	if err := a.cfg.Auth.RevokeInvitation(r.Context(), user(r), r.PathValue("id")); err != nil {
		return err
	}
	if a.cfg.OnInvitationsChanged != nil {
		a.cfg.OnInvitationsChanged()
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) erase(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Scope       string `json:"scope"`
		ID          string `json:"id"`
		Reason      string `json:"reason"`
		CleanedText string `json:"cleaned_text"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	var err error
	switch in.Scope {
	case "comment":
		err = a.cfg.Admin.EraseComment(r.Context(), user(r), in.ID, in.Reason)
	case "attachment":
		err = a.cfg.Admin.EraseAttachment(r.Context(), user(r), in.ID, in.Reason)
	case "collab_document":
		_, err = a.cfg.Admin.EraseDocument(r.Context(), user(r), in.ID, in.CleanedText, in.Reason)
	default:
		err = fmt.Errorf("%w: scope must be comment, attachment or collab_document", errBadRequest)
	}
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// export streams the archive as the response. The digest the service
// records is sent as a trailer, since it is known only at the end.
func (a *API) export(w http.ResponseWriter, r *http.Request) error {
	if !user(r).IsAdmin() {
		return auth.ErrForbidden
	}
	name := r.PathValue("project")
	if _, err := a.cfg.Projects.Get(r.Context(), user(r), name); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + "-export.zip"}))
	w.Header().Set("Trailer", "X-Garret-Export-SHA256")
	sum, err := a.cfg.Admin.Export(r.Context(), user(r), name, w)
	if err != nil {
		// Headers are already sent; the missing trailer tells the client the
		// archive is incomplete.
		a.cfg.Log.Error("export failed", "project", name, "err", err)
		return nil
	}
	w.Header().Set("X-Garret-Export-SHA256", sum)
	return nil
}

func (a *API) purge(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ConfirmName string `json:"confirm_name"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if err := a.cfg.Admin.Purge(r.Context(), user(r), r.PathValue("project"), in.ConfirmName); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
