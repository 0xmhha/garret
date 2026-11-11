-- Baseline migration. It creates no tables; it marks the point from which the
-- schema in docs/specs/data.md is built by the migrations that follow.

-- +goose Up
SELECT 1;

-- +goose Down
SELECT 1;
