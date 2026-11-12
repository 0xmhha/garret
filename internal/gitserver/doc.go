// Package gitserver serves Git's smart HTTP protocol for the projects'
// repositories. Git does the pack negotiation (upload-pack and receive-pack
// in stateless RPC mode); this package authenticates the request, bounds it
// and detects which branches a push moved.
//
// See docs/specs/transport.md section 4.
package gitserver
