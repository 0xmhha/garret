-- name: InsertSession :exec
INSERT INTO sessions (id_sha256, user_id, certificate_serial, csrf_token_sha256, created_at, last_seen_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetSession :one
SELECT * FROM sessions WHERE id_sha256 = ?;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = ? WHERE id_sha256 = ? AND last_seen_at < ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id_sha256 = ?;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = ?;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at <= ?;

-- name: UpdatePasswordHash :exec
UPDATE users SET password_hash = ? WHERE id = ?;

-- name: DisableUser :execrows
UPDATE users SET disabled_at = ? WHERE id = ? AND disabled_at IS NULL;

-- name: RevokeUserCertificates :exec
UPDATE client_certificates SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL;

-- name: InsertAccessToken :exec
INSERT INTO access_tokens (id, user_id, name, token_sha256, created_at) VALUES (?, ?, ?, ?, ?);

-- name: GetAccessTokenByDigest :one
SELECT * FROM access_tokens WHERE token_sha256 = ?;

-- name: GetAccessToken :one
SELECT * FROM access_tokens WHERE id = ?;

-- name: ListUserAccessTokens :many
SELECT * FROM access_tokens WHERE user_id = ? ORDER BY created_at DESC;

-- name: TouchAccessToken :exec
UPDATE access_tokens SET last_used_at = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?);

-- name: RevokeAccessToken :execrows
UPDATE access_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL;

-- name: RevokeUserAccessTokens :exec
UPDATE access_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL;
