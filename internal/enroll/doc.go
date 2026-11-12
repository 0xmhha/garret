// Package enroll is the client side of garret enroll and garret renew: it
// verifies the server's CA against the invitation link, generates the device
// key, requests and stores the client certificate, and points Git at it.
//
// See docs/specs/transport.md section 6.
package enroll
