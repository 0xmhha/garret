package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/0xmhha/garret/internal/admin"
	"github.com/0xmhha/garret/internal/attachment"
	"github.com/0xmhha/garret/internal/auth"
	"github.com/0xmhha/garret/internal/collab"
	"github.com/0xmhha/garret/internal/comment"
	"github.com/0xmhha/garret/internal/discussion"
	"github.com/0xmhha/garret/internal/gitrepo"
	"github.com/0xmhha/garret/internal/inbox"
	"github.com/0xmhha/garret/internal/project"
	"github.com/0xmhha/garret/internal/review"
)

// API error codes (transport.md 3.2). They are stable and documented.
const (
	codeUnauthenticated   = "unauthenticated"
	codeForbidden         = "forbidden"
	codeNotFound          = "not_found"
	codeConflict          = "conflict"
	codeInvalid           = "invalid"
	codeTooLarge          = "too_large"
	codeInvalidInvitation = "invalid_invitation"
	codeExportRequired    = "export_required"
	codeInternal          = "internal"
)

// errorMapping lists, in order, which service errors produce which status.
// One generic message per code: internal error text never reaches clients.
var errorMapping = []struct {
	errs    []error
	status  int
	code    string
	message string
}{
	{[]error{auth.ErrInvalidCredentials, auth.ErrUnauthenticated}, http.StatusUnauthorized, codeUnauthenticated, "sign in again"},
	{[]error{auth.ErrInvalidInvitation}, http.StatusBadRequest, codeInvalidInvitation, "the invitation code is not valid"},
	{[]error{admin.ErrExportRequired}, http.StatusConflict, codeExportRequired, "export the project first"},
	{[]error{auth.ErrRenewalRefused}, http.StatusConflict, "renewal_refused",
		"renewal refused: the certificate is not due yet, or it was already renewed; if this device did not renew it, revoke the device"},
	{[]error{errNotJSON}, http.StatusUnsupportedMediaType, "unsupported_media_type", "send application/json"},
	{[]error{errTooManyConnections}, http.StatusTooManyRequests, "too_many_connections", "too many open connections"},
	{[]error{auth.ErrForbidden, project.ErrForbidden, review.ErrForbidden, comment.ErrForbidden, discussion.ErrForbidden,
		attachment.ErrForbidden, admin.ErrForbidden}, http.StatusForbidden, codeForbidden, "not allowed"},
	{[]error{auth.ErrNotFound, project.ErrNotFound, review.ErrNotFound, comment.ErrNotFound, discussion.ErrNotFound,
		attachment.ErrNotFound, admin.ErrNotFound, collab.ErrNotFound}, http.StatusNotFound, codeNotFound, "not found"},
	{[]error{auth.ErrConflict, project.ErrConflict, project.ErrNoSource, review.ErrConflict, comment.ErrConflict, admin.ErrConflict, collab.ErrStale},
		http.StatusConflict, codeConflict, "the resource changed; reload and try again"},
	{[]error{attachment.ErrTooLarge, gitrepo.ErrTooLarge, collab.ErrTooLarge, errBodyTooLarge}, http.StatusRequestEntityTooLarge, codeTooLarge, "too large"},
	{[]error{auth.ErrInvalid, project.ErrInvalid, review.ErrInvalid, comment.ErrInvalid, discussion.ErrInvalid,
		admin.ErrInvalid, inbox.ErrInvalid, auth.ErrHashFormat, errBadRequest}, http.StatusBadRequest, codeInvalid, ""},
}

var (
	errBadRequest   = errors.New("httpapi: malformed request")
	errBodyTooLarge = errors.New("httpapi: request body too large")
	errNotJSON      = errors.New("httpapi: content type must be application/json")
)

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// writeError maps err to a status and code. For invalid input the message
// is the service's own validation text, which names the field and never
// contains secrets; everything else gets a fixed message. Unmapped errors
// are logged and answered with 500.
func writeError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	var body errorBody
	status := http.StatusInternalServerError
	body.Error.Code, body.Error.Message = codeInternal, "internal error"
	for _, m := range errorMapping {
		if matchesAny(err, m.errs) {
			status, body.Error.Code, body.Error.Message = m.status, m.code, m.message
			if body.Error.Message == "" {
				body.Error.Message = err.Error()
			}
			break
		}
	}
	if status == http.StatusInternalServerError {
		log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	}
	writeJSON(w, status, body)
}

func matchesAny(err error, targets []error) bool {
	for _, t := range targets {
		if errors.Is(err, t) {
			return true
		}
	}
	return false
}
