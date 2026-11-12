-- name: InsertAuditEvent :exec
INSERT INTO audit_events (id, project_id, actor_id, action, subject_kind, subject_id, data, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);
