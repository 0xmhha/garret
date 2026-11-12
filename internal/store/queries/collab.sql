-- name: GetCollabDocument :one
SELECT * FROM collab_documents WHERE id = ?;

-- name: LatestCheckpoint :one
SELECT * FROM collab_checkpoints WHERE document_id = ? ORDER BY up_to_seq DESC LIMIT 1;

-- name: ListUpdatesAfter :many
SELECT * FROM collab_updates WHERE document_id = ? AND seq > ? ORDER BY seq;

-- name: AllocateUpdateSeq :one
UPDATE collab_documents SET next_seq = next_seq + 1 WHERE id = ? RETURNING next_seq - 1;

-- name: InsertCollabUpdate :exec
INSERT INTO collab_updates (document_id, seq, user_id, received_at, payload) VALUES (?, ?, ?, ?, ?);

-- name: InsertCheckpoint :exec
INSERT INTO collab_checkpoints (document_id, up_to_seq, state, text, contributors, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetReviewByDescriptionDoc :one
SELECT * FROM reviews WHERE description_doc_id = ?;

-- name: GetDiscussionByBodyDoc :one
SELECT * FROM discussions WHERE body_doc_id = ?;

-- name: DeleteDocumentUpdates :exec
DELETE FROM collab_updates WHERE document_id = ?;

-- name: DeleteDocumentCheckpoints :exec
DELETE FROM collab_checkpoints WHERE document_id = ?;

-- name: BumpDocumentGeneration :one
UPDATE collab_documents SET generation = generation + 1 WHERE id = ? RETURNING generation, next_seq;

-- name: ListCheckpointsAfter :many
SELECT up_to_seq, text, contributors, created_at FROM collab_checkpoints
WHERE document_id = @document_id AND up_to_seq > @after
ORDER BY up_to_seq
LIMIT @max_rows;
