// Package httpapi serves garret's JSON API (docs/design/api.md). Handlers
// parse and validate requests, call one service method with the request's
// user, and map the result; they never touch the store.
//
// See docs/specs/transport.md section 3.
package httpapi
