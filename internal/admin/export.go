package admin

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// exportTable is one table of the archive and the statement that selects
// the project's rows. The export reads generic columns instead of sqlc
// structs so every column, present and future, is kept as stored.
type exportTable struct {
	name  string
	query string
}

// exportTables lists every table with project data, plus the users the
// rows refer to. Password hashes and other credentials are never exported.
var exportTables = []exportTable{
	{"projects", "SELECT * FROM projects WHERE id = ?1"},
	{"users", "SELECT id, username, display_name, role, created_at, disabled_at FROM users WHERE ?1 = ?1 ORDER BY username"},
	{"reviews", "SELECT * FROM reviews WHERE project_id = ?1 ORDER BY number"},
	{"revisions", "SELECT v.* FROM revisions v JOIN reviews r ON r.id = v.review_id WHERE r.project_id = ?1 ORDER BY v.review_id, v.number"},
	{"revision_commits", "SELECT c.* FROM revision_commits c JOIN revisions v ON v.id = c.revision_id JOIN reviews r ON r.id = v.review_id WHERE r.project_id = ?1 ORDER BY c.revision_id, c.position"},
	{"verdicts", "SELECT x.* FROM verdicts x JOIN reviews r ON r.id = x.review_id WHERE r.project_id = ?1 ORDER BY x.id"},
	{"review_state_changes", "SELECT x.* FROM review_state_changes x JOIN reviews r ON r.id = x.review_id WHERE r.project_id = ?1 ORDER BY x.id"},
	{"discussions", "SELECT * FROM discussions WHERE project_id = ?1 ORDER BY id"},
	{"comments", "SELECT * FROM comments WHERE project_id = ?1 ORDER BY id"},
	{"comment_versions", "SELECT v.* FROM comment_versions v JOIN comments c ON c.id = v.comment_id WHERE c.project_id = ?1 ORDER BY v.comment_id, v.version"},
	{"comment_attachments", "SELECT x.* FROM comment_attachments x JOIN comments c ON c.id = x.comment_id WHERE c.project_id = ?1 ORDER BY x.comment_id, x.attachment_id"},
	{"attachments", "SELECT * FROM attachments WHERE project_id = ?1 ORDER BY id"},
	{"collab_documents", "SELECT * FROM collab_documents WHERE project_id = ?1 ORDER BY id"},
	{"collab_updates", "SELECT u.* FROM collab_updates u JOIN collab_documents d ON d.id = u.document_id WHERE d.project_id = ?1 ORDER BY u.document_id, u.seq"},
	{"collab_checkpoints", "SELECT k.* FROM collab_checkpoints k JOIN collab_documents d ON d.id = k.document_id WHERE d.project_id = ?1 ORDER BY k.document_id, k.up_to_seq"},
	{"notifications", "SELECT * FROM notifications WHERE project_id = ?1 ORDER BY id"},
	{"audit_events", "SELECT * FROM audit_events WHERE project_id = ?1 ORDER BY id"},
}

// writeTable writes the rows of t as a JSON array of objects. BLOB columns
// become base64 strings.
func writeTable(ctx context.Context, tx *sql.Tx, zw *zip.Writer, t exportTable, projectID string) error {
	rows, err := tx.QueryContext(ctx, t.query, projectID)
	if err != nil {
		return fmt.Errorf("admin: export %s: %w", t.name, err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	out := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = values[i]
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	w, err := zw.Create("tables/" + t.name + ".json")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	return enc.Encode(out)
}

func attachmentDigests(ctx context.Context, tx *sql.Tx, projectID string) ([][]byte, error) {
	rows, err := tx.QueryContext(ctx, "SELECT DISTINCT sha256 FROM attachments WHERE project_id = ? ORDER BY sha256", projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out [][]byte
	for rows.Next() {
		var d []byte
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
