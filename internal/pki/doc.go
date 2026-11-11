// Package pki holds garret's certificate primitives: serial numbers,
// fingerprints, the CA, server and client certificate templates, signing,
// certificate request checks and the renewal rule.
//
// The functions here do no I/O. Storing keys and running rotation are built
// on them in later stages. See docs/design/security.md, "Transport security
// and device certificates", and docs/specs/atomic.md section 5.
package pki
