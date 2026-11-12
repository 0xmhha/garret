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
}
