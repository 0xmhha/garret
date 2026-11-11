package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// migrationsDir is the directory inside migrationFiles that holds the goose SQL files.
const migrationsDir = "migrations"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// ErrNilDB is returned when Migrate is called without a database.
var ErrNilDB = errors.New("store: nil database")

// ErrSchemaTooNew is returned when the database was migrated by a newer
// binary than this one. Running would risk writing with an outdated schema.
var ErrSchemaTooNew = errors.New("store: database schema is newer than this binary")

// LatestSchemaVersion returns the highest migration version embedded in the
// binary. It does not touch any database.
func LatestSchemaVersion() (int64, error) {
	sources, err := migrationSources()
	if err != nil {
		return 0, err
	}
	return sources[len(sources)-1].Version, nil
}

// Migrate applies every embedded migration that db has not applied yet.
//
// It returns ErrNilDB when db is nil and ErrSchemaTooNew when db already has
// a schema version above LatestSchemaVersion; in both cases it changes
// nothing. Running it again on a migrated database is a no-op. Each migration
// runs in its own transaction, so a failure leaves the database at the last
// fully applied version. Migrate must not run concurrently with itself on the
// same database; the caller runs it once at startup before serving requests.
func Migrate(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return ErrNilDB
	}
	provider, err := newProvider(db)
	if err != nil {
		return err
	}
	latest, err := LatestSchemaVersion()
	if err != nil {
		return err
	}
	current, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	if current > latest {
		return fmt.Errorf("%w: database at %d, binary knows up to %d", ErrSchemaTooNew, current, latest)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("store: apply migrations: %w", err)
	}
	return nil
}

func newProvider(db *sql.DB) (*goose.Provider, error) {
	sub, err := fs.Sub(migrationFiles, migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("store: open embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		return nil, fmt.Errorf("store: load migrations: %w", err)
	}
	return provider, nil
}

func migrationSources() ([]*goose.Source, error) {
	// A provider needs a *sql.DB but listing sources never uses it, so a
	// closed handle is enough and keeps this function free of I/O.
	db, err := sql.Open(sqliteDriverName, "")
	if err != nil {
		return nil, fmt.Errorf("store: prepare migration listing: %w", err)
	}
	defer func() { _ = db.Close() }()
	provider, err := newProvider(db)
	if err != nil {
		return nil, err
	}
	sources := provider.ListSources()
	if len(sources) == 0 {
		return nil, goose.ErrNoMigrations
	}
	return sources, nil
}
