-- name: CountReplies :one
SELECT count(*) FROM comments WHERE thread_root_id = ?;

-- name: DeleteCommentAttachmentsOfComment :exec
DELETE FROM comment_attachments WHERE comment_id = ?;

-- name: DeleteCommentVersions :exec
DELETE FROM comment_versions WHERE comment_id = ?;

-- name: MakeCommentPlaceholder :exec
UPDATE comments SET current_version = 0, deleted_at = COALESCE(deleted_at, sqlc.arg(now)),
    deleted_by = COALESCE(deleted_by, sqlc.arg(actor)) WHERE id = sqlc.arg(id);

-- name: DeleteComment :exec
DELETE FROM comments WHERE id = ?;

-- name: DeleteNotificationsAbout :exec
DELETE FROM notifications WHERE subject_id = ?;

-- name: InsertErasure :exec
INSERT INTO erasures (id, actor_id, scope, subject_id, subject_label, reason, created_at) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: DeleteCommentAttachmentsOfAttachment :exec
DELETE FROM comment_attachments WHERE attachment_id = ?;

-- name: DeleteAttachment :exec
DELETE FROM attachments WHERE id = ?;

-- name: CountAttachmentsWithDigest :one
SELECT count(*) FROM attachments WHERE project_id = ? AND sha256 = ?;

-- name: LatestProjectEvent :one
SELECT action FROM audit_events WHERE project_id = ? ORDER BY id DESC LIMIT 1;

-- Purge deletes children before parents (data.md 6.6).

-- name: PurgeNotifications :exec
DELETE FROM notifications WHERE project_id = ?;

-- name: PurgeCommentAttachments :exec
DELETE FROM comment_attachments WHERE comment_id IN (SELECT c.id FROM comments c WHERE c.project_id = ?);

-- name: PurgeCommentVersions :exec
DELETE FROM comment_versions WHERE comment_id IN (SELECT c.id FROM comments c WHERE c.project_id = ?);

-- name: PurgeReplies :exec
DELETE FROM comments WHERE project_id = ? AND thread_root_id IS NOT NULL;

-- name: PurgeComments :exec
DELETE FROM comments WHERE project_id = ?;

-- name: PurgeCheckpoints :exec
DELETE FROM collab_checkpoints WHERE document_id IN (SELECT d.id FROM collab_documents d WHERE d.project_id = ?);

-- name: PurgeUpdates :exec
DELETE FROM collab_updates WHERE document_id IN (SELECT d.id FROM collab_documents d WHERE d.project_id = ?);

-- name: PurgeVerdicts :exec
DELETE FROM verdicts WHERE review_id IN (SELECT r.id FROM reviews r WHERE r.project_id = ?);

-- name: PurgeStateChanges :exec
DELETE FROM review_state_changes WHERE review_id IN (SELECT r.id FROM reviews r WHERE r.project_id = ?);

-- name: PurgeRevisionCommits :exec
DELETE FROM revision_commits WHERE revision_id IN
    (SELECT v.id FROM revisions v JOIN reviews r ON r.id = v.review_id WHERE r.project_id = ?);

-- name: PurgeRevisions :exec
DELETE FROM revisions WHERE review_id IN (SELECT r.id FROM reviews r WHERE r.project_id = ?);

-- name: PurgeReviews :exec
DELETE FROM reviews WHERE project_id = ?;

-- name: PurgeDiscussions :exec
DELETE FROM discussions WHERE project_id = ?;

-- name: PurgeCollabDocuments :exec
DELETE FROM collab_documents WHERE project_id = ?;

-- name: PurgeAttachments :exec
DELETE FROM attachments WHERE project_id = ?;

-- name: PurgeAuditEvents :exec
DELETE FROM audit_events WHERE project_id = ?;

-- name: PurgeProject :exec
DELETE FROM projects WHERE id = ?;

-- name: LatestProjectEventID :one
SELECT CAST(COALESCE(max(id), '') AS TEXT) FROM audit_events WHERE project_id = ?;

-- name: LatestProjectEventWithData :one
SELECT action, data FROM audit_events WHERE project_id = ? ORDER BY id DESC LIMIT 1;
