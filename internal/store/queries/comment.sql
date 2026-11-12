-- name: InsertComment :exec
INSERT INTO comments (id, project_id, review_id, discussion_id, thread_root_id, author_id,
                      anchor_revision_id, anchor_view, anchor_from_sha, anchor_to_sha, anchor_path, anchor_side,
                      anchor_start_line, anchor_end_line, resolved, current_version, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 1, ?);

-- name: InsertCommentVersion :exec
INSERT INTO comment_versions (comment_id, version, body, created_at) VALUES (?, ?, ?, ?);

-- name: GetComment :one
SELECT * FROM comments WHERE id = ?;

-- name: GetCommentBody :one
SELECT body FROM comment_versions WHERE comment_id = ? AND version = ?;

-- name: BumpCommentVersion :execrows
UPDATE comments SET current_version = current_version + 1
WHERE id = ? AND author_id = ? AND deleted_at IS NULL AND current_version = ?;

-- name: SetCommentResolved :execrows
UPDATE comments SET resolved = ? WHERE id = ? AND thread_root_id IS NULL AND deleted_at IS NULL;

-- name: SoftDeleteComment :execrows
UPDATE comments SET deleted_at = ?, deleted_by = ? WHERE id = ? AND deleted_at IS NULL;

-- name: ListCommentVersions :many
SELECT * FROM comment_versions WHERE comment_id = ? ORDER BY version;

-- name: ListReviewComments :many
SELECT c.*, COALESCE(v.body, '') AS body FROM comments c
LEFT JOIN comment_versions v ON v.comment_id = c.id AND v.version = c.current_version
WHERE c.review_id = ? ORDER BY c.created_at, c.id;

-- name: ListDiscussionComments :many
SELECT c.*, COALESCE(v.body, '') AS body FROM comments c
LEFT JOIN comment_versions v ON v.comment_id = c.id AND v.version = c.current_version
WHERE c.discussion_id = ? ORDER BY c.created_at, c.id;

-- name: GetRevisionByNumber :one
SELECT * FROM revisions WHERE review_id = ? AND number = ?;

-- name: ListEnabledUsersByName :many
SELECT id, username FROM users WHERE disabled_at IS NULL AND username IN (sqlc.slice('names'));

-- name: InsertDiscussion :exec
INSERT INTO discussions (id, project_id, title, author_id, body_doc_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetDiscussion :one
SELECT * FROM discussions WHERE id = ? AND project_id = ?;

-- name: ListDiscussions :many
SELECT * FROM discussions WHERE project_id = ? AND deleted_at IS NULL ORDER BY updated_at DESC;

-- name: RenameDiscussion :execrows
UPDATE discussions SET title = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL;

-- name: SoftDeleteDiscussion :execrows
UPDATE discussions SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL;

-- name: TouchDiscussion :exec
UPDATE discussions SET updated_at = ? WHERE id = ?;

-- name: GetReviewByNumberOrID :one
SELECT * FROM reviews WHERE id = ?;
