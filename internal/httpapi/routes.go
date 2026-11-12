package httpapi

import "net/http"

// routes registers every endpoint of docs/design/api.md implemented so far.
func (a *API) routes() {
	h := func(pattern string, handler http.Handler) { a.mux.Handle(pattern, handler) }
	h("POST "+basePath+"/auth/login", a.public(a.login))
	h("POST "+basePath+"/auth/logout", a.authed(a.logout))
	h("GET "+basePath+"/me", a.authed(a.me))
	h("GET "+basePath+"/me/tokens", a.authed(a.listTokens))
	h("POST "+basePath+"/me/tokens", a.authed(a.createToken))
	h("DELETE "+basePath+"/me/tokens/{id}", a.authed(a.revokeToken))
	h("GET "+basePath+"/me/certificates", a.authed(a.listCertificates))
	h("DELETE "+basePath+"/me/certificates/{serial}", a.authed(a.revokeCertificate))
	h("POST "+basePath+"/me/certificates/renew", a.authed(a.renewCertificate))

	p := basePath + "/projects"
	h("GET "+p, a.authed(a.listProjects))
	h("POST "+p, a.authed(a.createProject))
	h("GET "+p+"/{project}", a.authed(a.getProject))
	h("GET "+p+"/{project}/branches", a.authed(a.branches))

	rv := p + "/{project}/reviews"
	h("GET "+rv, a.authed(a.listReviews))
	h("POST "+rv, a.authed(a.openReview))
	h("GET "+rv+"/{n}", a.authed(a.getReview))
	h("POST "+rv+"/{n}/state", a.authed(a.changeState))
	h("POST "+rv+"/{n}/verdicts", a.authed(a.recordVerdict))
	h("GET "+rv+"/{n}/diff", a.authed(a.diff))
	h("GET "+rv+"/{n}/diff/file", a.authed(a.fileDiff))
	h("GET "+rv+"/{n}/comments", a.authed(a.reviewComments))
	h("POST "+rv+"/{n}/comments", a.authed(a.createReviewComment))

	ds := p + "/{project}/discussions"
	h("GET "+ds, a.authed(a.listDiscussions))
	h("POST "+ds, a.authed(a.createDiscussion))
	h("GET "+ds+"/{id}", a.authed(a.getDiscussion))
	h("PATCH "+ds+"/{id}", a.authed(a.renameDiscussion))
	h("DELETE "+ds+"/{id}", a.authed(a.deleteDiscussion))
	h("GET "+ds+"/{id}/comments", a.authed(a.discussionComments))
	h("POST "+ds+"/{id}/comments", a.authed(a.createDiscussionComment))

	c := basePath + "/comments/{id}"
	h("PATCH "+c, a.authed(a.editComment))
	h("POST "+c+"/resolve", a.authed(a.resolveComment))
	h("DELETE "+c, a.authed(a.deleteComment))
	h("GET "+c+"/versions", a.authed(a.commentVersions))
}
