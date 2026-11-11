// Package store owns garret's SQLite database: the schema migrations embedded
// in the binary and, in later stages, the generated queries and the
// repositories built on them.
//
// See docs/specs/data.md for the schema, the data lifecycle and the
// concurrency model this package implements.
package store
