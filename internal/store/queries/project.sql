-- name: InsertProject :exec
INSERT INTO projects (id, name, description, created_by, created_at) VALUES (?, ?, ?, ?, ?);

-- name: GetProjectByName :one
SELECT * FROM projects WHERE name = ?;

-- name: GetProjectByID :one
SELECT * FROM projects WHERE id = ?;

-- name: ListProjects :many
SELECT * FROM projects ORDER BY name;

-- name: ListProjectNames :many
SELECT name FROM projects;
