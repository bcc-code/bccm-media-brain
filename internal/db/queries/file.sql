-- name: CreateStorageLocation :one
INSERT INTO storage_location (name, kind, base_uri, online) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetStorageLocation :one
SELECT * FROM storage_location WHERE id = $1;

-- name: ListStorageLocations :many
SELECT * FROM storage_location ORDER BY name;

-- name: UpdateStorageLocation :one
UPDATE storage_location SET name = $2, kind = $3, base_uri = $4, online = $5
WHERE id = $1
  AND (sqlc.narg('expected_revision')::integer IS NULL OR revision = sqlc.narg('expected_revision'))
RETURNING *;

-- name: DeleteStorageLocation :execrows
DELETE FROM storage_location WHERE id = $1;

-- name: CreateFile :one
INSERT INTO file (size_bytes, mime_type, duration_ms, codec, width, height, probe)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: GetFile :one
SELECT * FROM file WHERE id = $1;

-- name: ListFiles :many
SELECT * FROM file WHERE (sqlc.narg('after')::text IS NULL OR id < sqlc.narg('after'))
ORDER BY id DESC LIMIT sqlc.arg('limit');

-- name: DeleteFile :execrows
DELETE FROM file WHERE id = $1;

-- name: GetFileByHash :one
SELECT f.* FROM file f JOIN file_hash h ON h.file_id = f.id WHERE h.algo = $1 AND h.value = $2;

-- name: AddFileHash :exec
INSERT INTO file_hash (file_id, algo, value) VALUES ($1, $2, $3);


-- name: CreateFileCopy :one
INSERT INTO file_copy (file_id, location_id, path, last_seen_at) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetFileCopy :one
SELECT * FROM file_copy WHERE id = $1;

-- name: GetFileCopyByPath :one
SELECT * FROM file_copy WHERE location_id = $1 AND path = $2;


-- name: SetFileCopyStatus :one
UPDATE file_copy SET status = $2, last_seen_at = $3, last_verified_at = $4 WHERE id = $1 RETURNING *;

-- name: DeleteFileCopy :execrows
DELETE FROM file_copy WHERE id = $1;

-- name: GetFiles :many
SELECT * FROM file WHERE id = ANY(@ids::text[]);

-- name: GetStorageLocations :many
SELECT * FROM storage_location WHERE id = ANY(@ids::text[]);

-- name: ListFileHashes :many
SELECT * FROM file_hash WHERE file_id = ANY(@file_ids::text[]) ORDER BY file_id, algo;

-- name: ListFileCopies :many
SELECT * FROM file_copy WHERE file_id = ANY(@file_ids::text[]) ORDER BY file_id, id;

-- name: GetStorageLocationByName :one
SELECT * FROM storage_location WHERE name = $1;
