// Package collab serves collaborative documents: review descriptions and
// discussion bodies edited by several people at once. One owner goroutine
// per loaded document holds the Yjs document (ygo), stores every update
// before applying and broadcasting it, and writes checkpoints after a quiet
// period.
//
// See docs/specs/services.md section 7 and docs/specs/data.md 6.5 and 7.5.
package collab
