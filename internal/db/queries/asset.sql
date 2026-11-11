-- name: ListAssetTypes :many
SELECT * FROM asset_type ORDER BY id;

-- name: GetAssetType :one
SELECT * FROM asset_type WHERE id = $1;

-- name: CreateAsset :one
INSERT INTO asset (type_id, title, description, captured_at, camera, reel, metadata, legacy_ids, original_language)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING *;

-- name: GetAsset :one
SELECT * FROM asset WHERE id = $1;

-- name: UpdateAsset :one
UPDATE asset SET type_id = $2, title = $3, description = $4, captured_at = $5, camera = $6, reel = $7,
    metadata = $8, legacy_ids = $9, original_language = $10, revision = revision + 1
WHERE id = $1
  AND (sqlc.narg('expected_revision')::integer IS NULL OR revision = sqlc.narg('expected_revision'))
RETURNING *;

-- name: DeleteAsset :execrows
DELETE FROM asset WHERE id = $1;

-- name: ListAssets :many
SELECT a.* FROM asset a
WHERE (sqlc.narg('type_id')::smallint IS NULL OR a.type_id = sqlc.narg('type_id'))
  AND (sqlc.narg('project_id')::text IS NULL OR EXISTS (
        SELECT 1 FROM asset_project ap WHERE ap.asset_id = a.id AND ap.project_id = sqlc.narg('project_id')))
  AND (sqlc.narg('title')::text IS NULL OR position(lower(sqlc.narg('title')) in lower(a.title)) > 0)
  AND (sqlc.narg('legacy_id')::text IS NULL OR a.legacy_ids @> ARRAY[sqlc.narg('legacy_id')::text])
  AND (@is_admin::bool OR EXISTS (
        SELECT 1 FROM asset_access aa WHERE aa.asset_id = a.id AND aa.principal_id = ANY(@principals::text[])
          AND (aa.expires_at IS NULL OR aa.expires_at > now())))
ORDER BY a.id DESC LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: LinkAssetProject :one
INSERT INTO asset_project (asset_id, project_id, role, linked_by) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: UnlinkAssetProject :execrows
DELETE FROM asset_project WHERE asset_id = $1 AND project_id = $2 AND role = $3;


-- name: CreateAssetVersion :one
INSERT INTO asset_version (asset_id, number, label, status, created_by)
SELECT @asset_id::text, coalesce(max(v.number), 0) + 1, sqlc.narg('label')::text, @status::version_status,
       sqlc.narg('created_by')::text
FROM asset_version v WHERE v.asset_id = @asset_id::text
RETURNING *;

-- name: GetAssetVersion :one
SELECT * FROM asset_version WHERE id = $1;

-- name: UpdateAssetVersion :one
UPDATE asset_version SET label = $2, status = $3, revision = revision + 1
WHERE id = $1
  AND (sqlc.narg('expected_revision')::integer IS NULL OR revision = sqlc.narg('expected_revision'))
RETURNING *;

-- name: DeleteAssetVersion :execrows
DELETE FROM asset_version WHERE id = $1;


-- name: CreateRendition :one
INSERT INTO rendition (version_id, kind, language, purpose, overrides, metadata)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: GetRendition :one
SELECT sqlc.embed(r), v.asset_id FROM rendition r
JOIN asset_version v ON v.id = r.version_id WHERE r.id = $1;

-- name: UpdateRendition :one
UPDATE rendition SET language = $2, purpose = $3, overrides = $4, metadata = $5, revision = revision + 1
WHERE id = $1
  AND (sqlc.narg('expected_revision')::integer IS NULL OR revision = sqlc.narg('expected_revision'))
RETURNING *;

-- name: DeleteRendition :execrows
DELETE FROM rendition WHERE id = $1;


-- name: FindSubtitleRendition :one
SELECT * FROM rendition
WHERE version_id = $1 AND kind = 'subtitle' AND language IS NOT DISTINCT FROM sqlc.narg('language')::text
ORDER BY id LIMIT 1;

-- name: AttachFile :one
INSERT INTO rendition_file (rendition_id, file_id, role, position) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: DetachFile :execrows
DELETE FROM rendition_file WHERE rendition_id = $1 AND file_id = $2 AND role = $3;


-- name: ListAssetProjects :many
SELECT * FROM asset_project WHERE asset_id = ANY(@asset_ids::text[]) ORDER BY asset_id, created_at, project_id, role;

-- name: ListAssetVersions :many
SELECT * FROM asset_version WHERE asset_id = ANY(@asset_ids::text[]) ORDER BY asset_id, number;

-- name: ListRenditions :many
SELECT * FROM rendition WHERE version_id = ANY(@version_ids::text[]) ORDER BY version_id, id;

-- name: ListRenditionFiles :many
SELECT * FROM rendition_file WHERE rendition_id = ANY(@rendition_ids::text[])
ORDER BY rendition_id, role, position, file_id;

-- name: GetAssetByLegacyID :one
SELECT * FROM asset WHERE legacy_ids @> ARRAY[@legacy_id::text] ORDER BY id LIMIT 1;

-- name: SetAssetMetadataKey :one
UPDATE asset SET metadata = jsonb_set(metadata, ARRAY[@key::text], @value::jsonb), revision = revision + 1
WHERE id = @id RETURNING *;

-- name: LockLegacyID :exec
-- Serialises imports of the same legacy id until the transaction ends.
SELECT pg_advisory_xact_lock(hashtextextended(@legacy_id::text, 0));
