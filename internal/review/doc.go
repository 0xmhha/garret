// Package review will own reviews, revisions, verdicts and comment porting.
//
// This stage holds only the pure porting functions: they move a comment's
// line range from one revision to the next using the diff between them. See
// docs/design/data-model.md, "Comment porting", and docs/specs/atomic.md
// section 8.
package review
