-- name: InsertAttachment :exec
INSERT INTO attachments (id, project_id, uploader_id, filename, media_type, size, sha256, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetAttachment :one
SELECT * FROM attachments WHERE id = ?;

-- name: SoftDeleteAttachment :execrows
UPDATE attachments SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL;

-- name: InsertCommentAttachment :exec
INSERT INTO comment_attachments (comment_id, attachment_id) VALUES (?, ?);

-- name: ListCommentAttachments :many
SELECT a.* FROM attachments a JOIN comment_attachments ca ON ca.attachment_id = a.id
WHERE ca.comment_id = ? ORDER BY a.created_at;
