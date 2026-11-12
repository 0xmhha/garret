// Package server holds garret's listeners and TLS configuration and derives
// each request's identity: the verified client certificate's serial and the
// TCP peer address, never values taken from headers.
//
// See docs/specs/transport.md section 2.
package server
