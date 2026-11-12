-- name: AllocateReviewNumber :one
UPDATE projects SET next_review_number = next_review_number + 1 WHERE id = ?
RETURNING next_review_number - 1;

-- name: InsertCollabDocument :exec
INSERT INTO collab_documents (id, project_id, kind, created_at) VALUES (?, ?, ?, ?);

-- name: InsertReview :exec
INSERT INTO reviews (id, project_id, number, title, source_branch, target_branch, state, author_id,
                     description_doc_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, 'open', ?, ?, ?, ?);

-- name: GetReviewByNumber :one
SELECT * FROM reviews WHERE project_id = ? AND number = ?;

-- name: ListReviews :many
SELECT * FROM reviews WHERE project_id = ? ORDER BY updated_at DESC, number DESC;

-- name: ListOpenReviewsOnBranch :many
SELECT * FROM reviews WHERE project_id = ? AND source_branch = ? AND state = 'open';

-- name: InsertRevision :exec
INSERT INTO revisions (id, review_id, number, base_sha, head_sha, commits_omitted, pushed_by, pushed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: InsertRevisionCommit :exec
INSERT INTO revision_commits (revision_id, position, sha, subject, author_name, authored_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: LatestRevision :one
SELECT * FROM revisions WHERE review_id = ? ORDER BY number DESC LIMIT 1;

-- name: ListRevisions :many
SELECT * FROM revisions WHERE review_id = ? ORDER BY number;

-- name: ListRevisionCommits :many
SELECT * FROM revision_commits WHERE revision_id = ? ORDER BY position;

-- name: TouchReview :exec
UPDATE reviews SET updated_at = ? WHERE id = ?;

-- name: InsertVerdict :exec
INSERT INTO verdicts (id, review_id, revision_id, user_id, value, created_at) VALUES (?, ?, ?, ?, ?, ?);

-- name: LatestVerdicts :many
SELECT v.* FROM verdicts v
WHERE v.review_id = ? AND v.id = (SELECT max(w.id) FROM verdicts w WHERE w.review_id = v.review_id AND w.user_id = v.user_id)
ORDER BY v.created_at;

-- name: ChangeReviewState :execrows
UPDATE reviews SET state = ?, updated_at = ? WHERE id = ? AND state = ?;

-- name: InsertStateChange :exec
INSERT INTO review_state_changes (id, review_id, from_state, to_state, user_id, created_at) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListRevisionPins :many
SELECT r.id AS revision_id, r.review_id, r.number, r.head_sha, p.name AS project_name
FROM revisions r JOIN reviews v ON v.id = r.review_id JOIN projects p ON p.id = v.project_id;
