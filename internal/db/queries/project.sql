-- name: CreateProgram :one
INSERT INTO program (code, name, description, metadata) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetProgram :one
SELECT * FROM program WHERE id = $1;

-- name: UpdateProgram :one
UPDATE program SET name = $2, description = $3, metadata = $4, revision = revision + 1
WHERE id = $1
  AND (sqlc.narg('expected_revision')::integer IS NULL OR revision = sqlc.narg('expected_revision'))
RETURNING *;

-- name: DeleteProgram :execrows
DELETE FROM program WHERE id = $1;

-- name: ListPrograms :many
SELECT p.* FROM program p
WHERE @is_admin::bool
   OR EXISTS (
        SELECT 1 FROM permission_grant g
        LEFT JOIN project pj ON g.scope = 'project' AND pj.id = g.scope_id
        WHERE ((g.scope = 'program' AND g.scope_id = p.id) OR pj.program_id = p.id)
          AND (g.user_id = ANY(@principals::text[]) OR g.group_id = ANY(@principals::text[]))
          AND (g.expires_at IS NULL OR g.expires_at > now()))
ORDER BY p.code LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CreateProject :one
INSERT INTO project (program_id, code, name, status, starts_on, ends_on, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: GetProject :one
SELECT * FROM project WHERE id = $1;

-- name: UpdateProject :one
UPDATE project SET name = $2, status = $3, starts_on = $4, ends_on = $5, metadata = $6, revision = revision + 1
WHERE id = $1
  AND (sqlc.narg('expected_revision')::integer IS NULL OR revision = sqlc.narg('expected_revision'))
RETURNING *;

-- name: DeleteProject :execrows
DELETE FROM project WHERE id = $1;

-- name: ListProjects :many
SELECT p.* FROM project p
WHERE (sqlc.narg('program_id')::text IS NULL OR p.program_id = sqlc.narg('program_id'))
  AND (sqlc.narg('status')::text IS NULL OR p.status::text = sqlc.narg('status'))
  AND (@is_admin::bool OR EXISTS (
        SELECT 1 FROM permission_grant g
        WHERE ((g.scope = 'project' AND g.scope_id = p.id) OR (g.scope = 'program' AND g.scope_id = p.program_id))
          AND (g.user_id = ANY(@principals::text[]) OR g.group_id = ANY(@principals::text[]))
          AND (g.expires_at IS NULL OR g.expires_at > now())))
ORDER BY p.code LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: GetPrograms :many
SELECT * FROM program WHERE id = ANY(@ids::text[]);

-- name: ListVisibleProjects :many
-- per_program and skip page the visible projects within each program, by code.
WITH visible AS (
    SELECT c.id, row_number() OVER (PARTITION BY c.program_id ORDER BY c.code) AS rn
    FROM project c
    WHERE (c.id = ANY(@ids::text[]) OR c.program_id = ANY(@program_ids::text[]))
      AND (@is_admin::bool OR EXISTS (
            SELECT 1 FROM permission_grant g
            WHERE ((g.scope = 'project' AND g.scope_id = c.id) OR (g.scope = 'program' AND g.scope_id = c.program_id))
              AND (g.user_id = ANY(@principals::text[]) OR g.group_id = ANY(@principals::text[]))
              AND (g.expires_at IS NULL OR g.expires_at > now())))
)
SELECT p.* FROM project p JOIN visible ON visible.id = p.id
WHERE visible.rn > sqlc.arg('skip')::int AND visible.rn <= sqlc.arg('skip')::int + sqlc.arg('per_program')::int
ORDER BY p.code;
