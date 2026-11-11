// Package gitrepo validates names that become Git paths or refs and parses
// the output of the git commands garret runs.
//
// The parsers read output produced with fixed flags (-z for paths), so they
// never unquote a path. See docs/specs/atomic.md section 7.
package gitrepo
