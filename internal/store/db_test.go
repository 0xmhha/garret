package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openDB(t *testing.T, opts ...Option) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "garret.db"), opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return db
}

func pragma(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	var v string
	if err := db.QueryRowContext(context.Background(), "PRAGMA "+name).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestOpenAppliesSettings(t *testing.T) {
	db := openDB(t)
	want := map[string]string{"foreign_keys": "1", "journal_mode": "wal", "synchronous": "1", "busy_timeout": "5000", "secure_delete": "1"}
	for name, v := range want {
		if got := pragma(t, db.writer, name); got != v {
			t.Errorf("writer %s = %s, want %s", name, got, v)
		}
	}
	if got := pragma(t, db.reader, "query_only"); got != "1" {
		t.Errorf("reader query_only = %s", got)
	}
	if got := pragma(t, db.reader, "foreign_keys"); got != "1" {
		t.Errorf("reader foreign_keys = %s", got)
	}
	if got := db.writer.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("writer max connections %d", got)
	}
	if got := db.reader.Stats().MaxOpenConnections; got < 2 || got > 8 {
		t.Errorf("reader max connections %d", got)
	}
	latest, err := LatestSchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if got := schemaVersion(t, db.writer); got != latest {
		t.Errorf("schema version %d, want %d", got, latest)
	}
}

func insertUser(tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(context.Background(), "INSERT INTO users VALUES (?, ?, 'N', 'h', 'member', 1, NULL)", id, "user-"+id)
	return err
}

func countUsers(t *testing.T, db *DB) int {
	t.Helper()
	var n int
	err := db.ReadTx(context.Background(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(context.Background(), "SELECT count(*) FROM users").Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWriteTx(t *testing.T) {
	db := openDB(t)
	if err := db.WriteTx(context.Background(), func(tx *sql.Tx) error { return insertUser(tx, "a") }); err != nil {
		t.Fatal(err)
	}
	if n := countUsers(t, db); n != 1 {
		t.Fatalf("users = %d after commit", n)
	}
	errStop := errors.New("stop")
	err := db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		if err := insertUser(tx, "b"); err != nil {
			return err
		}
		return errStop
	})
	if !errors.Is(err, errStop) {
		t.Fatalf("err = %v, want errStop unchanged", err)
	}
	if n := countUsers(t, db); n != 1 {
		t.Fatalf("users = %d after rollback", n)
	}
}

func TestWriteTxPanicRollsBack(t *testing.T) {
	db := openDB(t)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic did not propagate")
			}
		}()
		_ = db.WriteTx(context.Background(), func(tx *sql.Tx) error {
			_ = insertUser(tx, "a")
			panic("boom")
		})
	}()
	if n := countUsers(t, db); n != 0 {
		t.Fatalf("users = %d after panic", n)
	}
	if err := db.WriteTx(context.Background(), func(tx *sql.Tx) error { return insertUser(tx, "b") }); err != nil {
		t.Fatalf("writer unusable after panic: %v", err)
	}
}

func TestWriteTxWaitingContextCancelled(t *testing.T) {
	db := openDB(t)
	hold := make(chan struct{})
	started := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		_ = db.WriteTx(context.Background(), func(*sql.Tx) error {
			close(started)
			<-hold
			return nil
		})
	})
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	called := false
	err := db.WriteTx(ctx, func(*sql.Tx) error { called = true; return nil })
	close(hold)
	wg.Wait()
	if !errors.Is(err, context.DeadlineExceeded) || called {
		t.Fatalf("err = %v, called = %v", err, called)
	}
}

func TestWriteTxTimeout(t *testing.T) {
	db := openDB(t, WithWriteTimeout(50*time.Millisecond))
	err := db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		if err := insertUser(tx, "a"); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond) // outlive the transaction deadline on purpose
		return insertUser(tx, "b")
	})
	if err == nil {
		t.Fatal("transaction outlived its timeout")
	}
	if n := countUsers(t, db); n != 0 {
		t.Fatalf("users = %d after timeout", n)
	}
}

func TestWriteTxConcurrent(t *testing.T) {
	db := openDB(t)
	if err := db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), "CREATE TABLE counter (n INTEGER NOT NULL) STRICT; INSERT INTO counter VALUES (0)")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	const writers = 50
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for range writers {
		wg.Go(func() {
			errs <- db.WriteTx(context.Background(), func(tx *sql.Tx) error {
				var n int
				if err := tx.QueryRowContext(context.Background(), "SELECT n FROM counter").Scan(&n); err != nil {
					return err
				}
				_, err := tx.ExecContext(context.Background(), "UPDATE counter SET n = ?", n+1)
				return err
			})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := db.ReadTx(context.Background(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(context.Background(), "SELECT n FROM counter").Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	if n != writers {
		t.Fatalf("counter = %d, want %d", n, writers)
	}
}

func TestReadTxRejectsWrites(t *testing.T) {
	db := openDB(t)
	err := db.ReadTx(context.Background(), func(tx *sql.Tx) error { return insertUser(tx, "a") })
	if err == nil {
		t.Fatal("write through ReadTx succeeded")
	}
}

func TestCompact(t *testing.T) {
	db := openDB(t)
	if err := db.WriteTx(context.Background(), func(tx *sql.Tx) error { return insertUser(tx, "a") }); err != nil {
		t.Fatal(err)
	}
	if err := db.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := countUsers(t, db); n != 1 {
		t.Fatalf("users = %d after compact", n)
	}
}
