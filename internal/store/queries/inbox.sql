-- name: InsertNotification :exec
INSERT INTO notifications (id, user_id, project_id, kind, subject_kind, subject_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListNotifications :many
SELECT * FROM notifications
WHERE user_id = sqlc.arg(user_id) AND (sqlc.arg(unread_only) = 0 OR read_at IS NULL) AND id < sqlc.arg(before)
ORDER BY id DESC LIMIT sqlc.arg(page_size);

-- name: MarkNotificationRead :exec
UPDATE notifications SET read_at = ? WHERE id = ? AND user_id = ? AND read_at IS NULL;

-- name: DeleteOldNotifications :execrows
DELETE FROM notifications WHERE created_at < ?;
