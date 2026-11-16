package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var schemaTables = []string{
	"access_tokens", "attachments", "audit_events", "client_certificates", "collab_checkpoints",
	"collab_documents", "collab_updates", "comment_attachments", "comment_versions", "comments",
	"discussions", "erasures", "invitations", "notifications", "projects", "review_state_changes",
	"reviews", "revision_commits", "revisions", "sessions", "users", "verdicts",
}

// migratedDB returns a migrated database with foreign keys enforced.
func migratedDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "garret.db") + "?_pragma=foreign_keys(1)"
	db, err := sql.Open(sqliteDriverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func exec(t *testing.T, db *sql.DB, q string, args ...any) error {
	t.Helper()
	_, err := db.ExecContext(context.Background(), q, args...)
	return err
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if err := exec(t, db, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func TestSchemaTablesAreStrict(t *testing.T) {
	db := migratedDB(t)
	rows, err := db.QueryContext(context.Background(),
		"SELECT name, strict FROM pragma_table_list WHERE schema = 'main' AND type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'goose_db_version' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		var strict int
		if err := rows.Scan(&name, &strict); err != nil {
			t.Fatal(err)
		}
		if strict != 1 {
			t.Errorf("table %s is not STRICT", name)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, schemaTables) {
		t.Fatalf("tables %v, want %v", names, schemaTables)
	}
}

const (
	sha40 = "0123456789abcdef0123456789abcdef01234567"
	dig32 = "x'0000000000000000000000000000000000000000000000000000000000000000'"
)

// seed inserts one user, project, collaborative documents, a review, a
// revision and a discussion that the constraint cases build on.
func seed(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, q := range []string{
		"INSERT INTO users VALUES ('U1', 'alice', 'Alice', 'h', 'admin', 1, NULL)",
		"INSERT INTO projects VALUES ('P1', 'garret', '', 'U1', 1, 1, NULL)",
		"INSERT INTO collab_documents VALUES ('D1', 'P1', 'review_description', 1, 1, 1)",
		"INSERT INTO collab_documents VALUES ('D2', 'P1', 'discussion_body', 1, 1, 1)",
		"INSERT INTO collab_documents VALUES ('D3', 'P1', 'review_description', 1, 1, 1)",
		"INSERT INTO reviews VALUES ('R1', 'P1', 1, 'Fix', 'refs/heads/fix', 'refs/heads/main', 'open', 'U1', 'D1', 1, 1)",
		"INSERT INTO revisions VALUES ('V1', 'R1', 1, '" + sha40 + "', '" + sha40 + "', 0, 'U1', 1)",
		"INSERT INTO discussions VALUES ('S1', 'P1', 'Plan', 'U1', 'D2', 1, 1, NULL)",
	} {
		mustExec(t, db, q)
	}
}

func TestSchemaConstraints(t *testing.T) {
	anchor := "'V1', 'revision', '" + sha40 + "', '" + sha40 + "', 'a.go', 'new'"
	tests := []struct {
		name string
		sql  string
		ok   bool
	}{
		{"review comment", "INSERT INTO comments VALUES ('C1','P1','R1',NULL,NULL,'U1',NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,0,1,1,NULL,NULL)", true},
		{"anchored line comment", "INSERT INTO comments VALUES ('C1','P1','R1',NULL,NULL,'U1'," + anchor + ",3,5,0,1,1,NULL,NULL)", true},
		{"anchored file comment", "INSERT INTO comments VALUES ('C1','P1','R1',NULL,NULL,'U1'," + anchor + ",NULL,NULL,0,1,1,NULL,NULL)", true},
		{"both parents", "INSERT INTO comments VALUES ('C1','P1','R1','S1',NULL,'U1',NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,0,1,1,NULL,NULL)", false},
		{"no parent", "INSERT INTO comments VALUES ('C1','P1',NULL,NULL,NULL,'U1',NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,0,1,1,NULL,NULL)", false},
		{"partial anchor", "INSERT INTO comments VALUES ('C1','P1','R1',NULL,NULL,'U1','V1',NULL,NULL,NULL,NULL,NULL,NULL,NULL,0,1,1,NULL,NULL)", false},
		{"start line without end", "INSERT INTO comments VALUES ('C1','P1','R1',NULL,NULL,'U1'," + anchor + ",3,NULL,0,1,1,NULL,NULL)", false},
		{"end before start", "INSERT INTO comments VALUES ('C1','P1','R1',NULL,NULL,'U1'," + anchor + ",5,3,0,1,1,NULL,NULL)", false},
		{"anchor on discussion", "INSERT INTO comments VALUES ('C1','P1',NULL,'S1',NULL,'U1'," + anchor + ",3,5,0,1,1,NULL,NULL)", false},
		{"deleted without actor", "INSERT INTO comments VALUES ('C1','P1','R1',NULL,NULL,'U1',NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,0,1,1,5,NULL)", false},
		{"second open review on a branch", "INSERT INTO reviews VALUES ('R2','P1',2,'Again','refs/heads/fix','refs/heads/main','open','U1','D3',1,1)", false},
		{"completed review on the same branch", "INSERT INTO reviews VALUES ('R2','P1',2,'Again','refs/heads/fix','refs/heads/main','completed','U1','D3',1,1)", true},
		{"invitation used without user", "INSERT INTO invitations VALUES ('I1'," + dig32 + ",'member','U1',1,2,NULL,5,NULL)", false},
		{"short token digest", "INSERT INTO invitations VALUES ('I1',x'00','member','U1',1,2,NULL,NULL,NULL)", false},
		{"text in an integer column", "INSERT INTO projects VALUES ('P2','other','','U1','soon',1,NULL)", false},
		{"unknown user", "INSERT INTO projects VALUES ('P2','other','','U9',1,1,NULL)", false},
		{"relative source path", "INSERT INTO projects VALUES ('P2','other','','U1',1,1,'work/fix')", false},
		{"empty source path", "INSERT INTO projects VALUES ('P2','other','','U1',1,1,'')", false},
		{"absolute source path", "INSERT INTO projects VALUES ('P3','third','','U1',1,1,'/Users/me/fix')", true},
		{"audit data not an object", "INSERT INTO audit_events VALUES ('A1','P1','U1','review.created','review','R1','[]',1)", false},
		{"audit event", "INSERT INTO audit_events VALUES ('A1','P1','U1','review.created','review','R1','{}',1)", true},
		{"same state change", "INSERT INTO review_state_changes VALUES ('X1','R1','open','open','U1',1)", false},
		{"bad verdict", "INSERT INTO verdicts VALUES ('W1','R1','V1','U1','maybe',1)", false},
		{"update over 1 MiB", "INSERT INTO collab_updates VALUES ('D1',1,'U1',1,zeroblob(1048577))", false},
		{"short sha", "INSERT INTO revisions VALUES ('V2','R1',2,'abc','" + sha40 + "',0,'U1',1)", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := migratedDB(t)
			seed(t, db)
			err := exec(t, db, tt.sql)
			if tt.ok && err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestSchemaDeleteIsRestricted(t *testing.T) {
	db := migratedDB(t)
	seed(t, db)
	err := exec(t, db, "DELETE FROM projects WHERE id = 'P1'")
	if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatalf("err = %v, want a foreign key error", err)
	}
}

func TestSchemaDownRemovesEverything(t *testing.T) {
	db := migratedDB(t)
	provider, err := newProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(context.Background(),
		"SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'goose_db_version'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d tables left after down", n)
	}
}
