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

	h("POST "+p+"/{project}/attachments", a.authed(a.upload))
	h("GET "+basePath+"/attachments/{id}", a.authed(a.download))
	h("DELETE "+basePath+"/attachments/{id}", a.authed(a.deleteAttachment))

	h("GET "+basePath+"/inbox", a.authed(a.listInbox))
	h("POST "+basePath+"/inbox/read", a.authed(a.markRead))
	h("GET "+basePath+"/users", a.authed(a.listUsers))

	ad := basePath + "/admin"
	h("GET "+ad+"/invitations", a.authed(a.listInvitations))
	h("POST "+ad+"/invitations", a.authed(a.createInvitation))
	h("DELETE "+ad+"/invitations/{id}", a.authed(a.revokeInvitation))
	h("GET "+ad+"/users", a.authed(a.adminListUsers))
	h("POST "+ad+"/users/{id}/disable", a.authed(a.disableUser))
	h("POST "+ad+"/erasures", a.authed(a.erase))
	h("POST "+ad+"/projects/{project}/export", a.authed(a.export))
	h("POST "+ad+"/projects/{project}/purge", a.authed(a.purge))

	h("GET "+basePath+"/ws", a.authed(a.events))
	h("GET "+p+"/{project}/collab/{doc}", a.authed(a.collabDocument))
	h("GET "+p+"/{project}/collab/{doc}/info", a.authed(a.collabInfo))

	c := basePath + "/comments/{id}"
	h("PATCH "+c, a.authed(a.editComment))
	h("POST "+c+"/resolve", a.authed(a.resolveComment))
	h("DELETE "+c, a.authed(a.deleteComment))
	h("GET "+c+"/versions", a.authed(a.commentVersions))
}
