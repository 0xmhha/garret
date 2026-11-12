package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
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

// TestRenewalLineMigrationResolvesDuplicates covers a database written
// before renewals were limited to one per certificate: two certificates
// renewed from C0. The newest stays the line's successor; the other is
// revoked and detached, so the unique index can be created.
func TestRenewalLineMigrationResolvesDuplicates(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	p, err := newProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 2); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("k", 32)
	for _, q := range []string{
		`INSERT INTO users VALUES ('u1', 'bob', 'Bob', 'x', 'member', 1, NULL)`,
		`INSERT INTO client_certificates VALUES ('c0', 'u1', 'laptop', CAST('` + key + `' AS BLOB), 'invitation', NULL, 1, 100, NULL)`,
		`INSERT INTO client_certificates VALUES ('c1', 'u1', 'laptop', CAST('` + key + `' AS BLOB), 'renewal', 'c0', 50, 150, NULL)`,
		`INSERT INTO client_certificates VALUES ('c2', 'u1', 'laptop', CAST('` + key + `' AS BLOB), 'renewal', 'c0', 60, 160, NULL)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("migrate over duplicate renewals: %v", err)
	}
	rows := map[string]struct {
		from    sql.NullString
		revoked bool
	}{}
	r, err := db.QueryContext(ctx, `SELECT serial, renewed_from, revoked_at IS NOT NULL FROM client_certificates`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	for r.Next() {
		var s string
		var v struct {
			from    sql.NullString
			revoked bool
		}
		if err := r.Scan(&s, &v.from, &v.revoked); err != nil {
			t.Fatal(err)
		}
		rows[s] = v
	}
	if rows["c2"].from.String != "c0" || rows["c2"].revoked {
		t.Errorf("newest renewal changed: %+v", rows["c2"])
	}
	if rows["c1"].from.Valid || !rows["c1"].revoked {
		t.Errorf("older duplicate not detached and revoked: %+v", rows["c1"])
	}
	if rows["c0"].revoked {
		t.Errorf("the parent was revoked: %+v", rows["c0"])
	}
}
