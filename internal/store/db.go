package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"time"
)

// WriteTimeout bounds every write transaction (docs/specs/data.md, Q3), so
// one stuck operation cannot hold the write lock.
const WriteTimeout = 5 * time.Second

const (
	minReaders = 2
	maxReaders = 8
)

// connectionPragmas apply to every connection (docs/specs/data.md section 2).
var connectionPragmas = []string{
	"foreign_keys(1)",
	"synchronous(NORMAL)",
	"busy_timeout(5000)",
	"secure_delete(1)",
}

// DB is garret's database: one writer connection that owns every write and
// a pool of read-only connections. It is safe for concurrent use.
type DB struct {
	writer       *sql.DB
	reader       *sql.DB
	writeTimeout time.Duration
}

// Option configures Open.
type Option func(*DB)

// WithWriteTimeout replaces WriteTimeout. Tests use it to exercise timeouts.
func WithWriteTimeout(d time.Duration) Option {
	return func(db *DB) { db.writeTimeout = d }
}

// Open opens the database at path, migrates it, and returns it. The caller
// must Close it.
func Open(ctx context.Context, path string, opts ...Option) (*DB, error) {
	db := &DB{writeTimeout: WriteTimeout}
	for _, o := range opts {
		o(db)
	}
	writer, err := sql.Open(sqliteDriverName, dsn(path, false))
	if err != nil {
		return nil, fmt.Errorf("store: open writer: %w", err)
	}
	writer.SetMaxOpenConns(1)
	if err := Migrate(ctx, writer); err != nil {
		return nil, errors.Join(err, writer.Close())
	}
	reader, err := sql.Open(sqliteDriverName, dsn(path, true))
	if err != nil {
		return nil, errors.Join(fmt.Errorf("store: open reader: %w", err), writer.Close())
	}
	reader.SetMaxOpenConns(max(minReaders, min(runtime.GOMAXPROCS(0), maxReaders)))
	db.writer, db.reader = writer, reader
	return db, nil
}

func dsn(path string, readOnly bool) string {
	q := url.Values{}
	for _, p := range connectionPragmas {
		q.Add("_pragma", p)
	}
	if readOnly {
		q.Set("mode", "ro")
		q.Add("_pragma", "query_only(1)")
	} else {
		q.Add("_pragma", "journal_mode(WAL)")
		q.Set("_txlock", "immediate")
	}
	return "file:" + path + "?" + q.Encode()
}

// Close closes the reader pool, then the writer.
func (db *DB) Close() error {
	return errors.Join(db.reader.Close(), db.writer.Close())
}

// WriteTx runs fn in one BEGIN IMMEDIATE transaction on the writer, bounded
// by the write timeout. It commits when fn returns nil and rolls back
// otherwise, returning fn's error unchanged. A panic in fn rolls back and
// propagates.
func (db *DB) WriteTx(ctx context.Context, fn func(*sql.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, db.writeTimeout)
	defer cancel()
	return runTx(ctx, db.writer, fn)
}

// ReadTx runs fn in a read-only transaction on the reader pool.
func (db *DB) ReadTx(ctx context.Context, fn func(*sql.Tx) error) error {
	return runTx(ctx, db.reader, fn)
}

func runTx(ctx context.Context, conn *sql.DB, fn func(*sql.Tx) error) (err error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}
