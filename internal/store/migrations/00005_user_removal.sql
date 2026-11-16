-- An administrator can remove a disabled user: the row stays for the
-- records that point at it (docs/design/decisions.md D18).

-- +goose Up
ALTER TABLE users ADD COLUMN removed_at INTEGER;

-- +goose Down
ALTER TABLE users DROP COLUMN removed_at;
