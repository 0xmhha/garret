package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(sqliteDriverName, filepath.Join(t.TempDir(), "garret.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return db
}

func schemaVersion(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var version int64
	err := db.QueryRowContext(context.Background(),
		"SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1").Scan(&version)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	return version
}

func TestMigrate(t *testing.T) {
	latest, err := LatestSchemaVersion()
	if err != nil {
		t.Fatalf("latest schema version: %v", err)
	}
	if latest < 1 {
		t.Fatalf("latest schema version = %d, want at least 1", latest)
	}

	tests := []struct {
		name        string
		prepare     func(t *testing.T, db *sql.DB)
		ctx         func() context.Context
		nilDB       bool
		wantErr     error
		wantAnyErr  bool
		wantVersion int64
	}{
		{
			name:        "fresh database reaches the latest version",
			wantVersion: latest,
		},
		{
			name: "second run changes nothing",
			prepare: func(t *testing.T, db *sql.DB) {
				if err := Migrate(context.Background(), db); err != nil {
					t.Fatalf("first migrate: %v", err)
				}
			},
			wantVersion: latest,
		},
		{
			name:    "nil database is rejected",
			nilDB:   true,
			wantErr: ErrNilDB,
		},
		{
			name: "cancelled context fails",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			wantAnyErr: true,
		},
		{
			name: "database newer than the binary is refused",
			prepare: func(t *testing.T, db *sql.DB) {
				if err := Migrate(context.Background(), db); err != nil {
					t.Fatalf("first migrate: %v", err)
				}
				_, err := db.ExecContext(context.Background(),
					"INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)", latest+1)
				if err != nil {
					t.Fatalf("simulate newer schema: %v", err)
				}
			},
			wantErr:     ErrSchemaTooNew,
			wantVersion: latest + 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			if tc.prepare != nil {
				tc.prepare(t, db)
			}
			ctx := context.Background()
			if tc.ctx != nil {
				ctx = tc.ctx()
			}
			target := db
			if tc.nilDB {
				target = nil
			}

			err := Migrate(ctx, target)

			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Migrate() error = %v, want %v", err, tc.wantErr)
				}
			case tc.wantAnyErr:
				if err == nil {
					t.Fatal("Migrate() error = nil, want an error")
				}
			default:
				if err != nil {
					t.Fatalf("Migrate() error = %v", err)
				}
			}
			if tc.wantVersion != 0 {
				if got := schemaVersion(t, db); got != tc.wantVersion {
					t.Fatalf("schema version = %d, want %d", got, tc.wantVersion)
				}
			}
		})
	}
}
