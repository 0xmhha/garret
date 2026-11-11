// Package netguard decides which networks may reach garret and checks each
// connection's peer address against them.
//
// The functions here are pure. Listing the host's interfaces and filtering
// accepted connections are built on them in later stages. See
// docs/design/security.md, "Network boundary", and docs/specs/atomic.md
// section 4.
package netguard
