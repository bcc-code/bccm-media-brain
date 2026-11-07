-- name: GetDummy :one
SELECT * FROM dummy WHERE id = $1;

-- name: ListDummies :many
SELECT * FROM dummy ORDER BY id LIMIT $1 OFFSET $2;

-- name: CreateDummy :one
INSERT INTO dummy (name) VALUES ($1) RETURNING *;

-- name: DeleteDummy :execrows
DELETE FROM dummy WHERE id = $1;
