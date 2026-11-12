// Package realtime fans out change events to connected browsers. One hub
// goroutine owns every subscription; publishers never block, and a slow
// subscriber is dropped instead of slowing anyone else down.
//
// See docs/specs/data.md section 7.6 and docs/specs/services.md section 2.
package realtime
