-- name: CountActiveAdmins :one
SELECT count(*) FROM users WHERE role = 'admin' AND disabled_at IS NULL;

-- name: InsertUser :exec
INSERT INTO users (id, username, display_name, password_hash, role, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY username;

-- name: InsertInvitation :exec
INSERT INTO invitations (id, token_sha256, role, created_by, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetInvitationByDigest :one
SELECT * FROM invitations WHERE token_sha256 = ?;

-- name: UseInvitation :execrows
UPDATE invitations SET used_at = ?, used_by = ?
WHERE id = ? AND used_at IS NULL AND revoked_at IS NULL AND expires_at > ?;

-- name: RevokeInvitation :execrows
UPDATE invitations SET revoked_at = ? WHERE id = ? AND used_at IS NULL AND revoked_at IS NULL;

-- name: ListInvitations :many
SELECT * FROM invitations ORDER BY created_at DESC;

-- name: EarliestOpenInvitationExpiry :one
SELECT CAST(COALESCE(min(expires_at), 0) AS INTEGER) FROM invitations
WHERE used_at IS NULL AND revoked_at IS NULL AND expires_at > ?;

-- name: InsertCertificate :exec
INSERT INTO client_certificates
    (serial, user_id, device_name, public_key_sha256, issued_via, renewed_from, not_before, not_after)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetCertificate :one
SELECT * FROM client_certificates WHERE serial = ?;

-- name: ListUserCertificates :many
SELECT * FROM client_certificates WHERE user_id = ? ORDER BY not_before DESC;

-- name: RevokeCertificate :execrows
UPDATE client_certificates SET revoked_at = ? WHERE serial = ? AND revoked_at IS NULL;

-- name: ListActiveSerials :many
SELECT c.serial FROM client_certificates c JOIN users u ON u.id = c.user_id
WHERE c.revoked_at IS NULL AND u.disabled_at IS NULL;
