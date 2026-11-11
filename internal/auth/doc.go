// Package auth holds garret's credential primitives: random secrets and
// their digests, password hashing and the account naming rules.
//
// The functions here are pure apart from reading the injected random source.
// See docs/design/security.md, "Authentication", and docs/specs/atomic.md
// section 6.
package auth
