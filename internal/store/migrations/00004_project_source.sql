-- A project can sync from a Git repository on the server's machine
-- (docs/design/decisions.md D15). NULL means the project has no source.

-- +goose Up
ALTER TABLE projects ADD COLUMN source_path TEXT
    CHECK (source_path IS NULL OR (length(source_path) BETWEEN 1 AND 4096 AND substr(source_path, 1, 1) = '/'));

-- +goose Down
ALTER TABLE projects DROP COLUMN source_path;
