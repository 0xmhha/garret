-- name: InsertNotification :exec
INSERT INTO notifications (id, user_id, project_id, kind, subject_kind, subject_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListNotifications :many
-- Each row also carries where it points: the project's name, and the
-- review number or discussion ID its subject belongs to.
SELECT n.id, n.user_id, n.project_id, n.kind, n.subject_kind, n.subject_id, n.created_at, n.read_at,
    p.name AS project_name,
    CAST(COALESCE(rv.number, rvc.number, rvr.number, 0) AS INTEGER) AS review_number,
    CAST(COALESCE(d.id, dc.id, '') AS TEXT) AS discussion_id
FROM notifications n
JOIN projects p ON p.id = n.project_id
LEFT JOIN reviews rv ON n.subject_kind = 'review' AND rv.id = n.subject_id
LEFT JOIN comments c ON n.subject_kind = 'comment' AND c.id = n.subject_id
LEFT JOIN reviews rvc ON rvc.id = c.review_id
LEFT JOIN revisions r2 ON n.subject_kind = 'revision' AND r2.id = n.subject_id
LEFT JOIN reviews rvr ON rvr.id = r2.review_id
LEFT JOIN discussions d ON n.subject_kind = 'discussion' AND d.id = n.subject_id
LEFT JOIN discussions dc ON dc.id = c.discussion_id
WHERE n.user_id = sqlc.arg(user_id) AND (sqlc.arg(unread_only) = 0 OR n.read_at IS NULL) AND n.id < sqlc.arg(before)
ORDER BY n.id DESC LIMIT sqlc.arg(page_size);

-- name: MarkNotificationRead :exec
UPDATE notifications SET read_at = ? WHERE id = ? AND user_id = ? AND read_at IS NULL;

-- name: DeleteOldNotifications :execrows
DELETE FROM notifications WHERE created_at < ?;
