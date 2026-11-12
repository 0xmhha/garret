// Package audit records the append-only audit trail. Every service writes its
// events through Record inside its own write transaction, so an event exists
// exactly when the change it describes was committed.
//
// See docs/specs/services.md section 2.
package audit
