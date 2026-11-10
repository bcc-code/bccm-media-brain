-- name: GetUser :one
SELECT * FROM app_user WHERE id = $1;

-- name: GetUserByExternalID :one
SELECT * FROM app_user WHERE external_id = $1;

-- name: GetUserByEmail :one
SELECT * FROM app_user WHERE lower(email) = lower(@email::text);

-- name: UpsertUser :one
INSERT INTO app_user (external_id, email, name)
VALUES (@external_id, sqlc.narg('email'), sqlc.narg('name'))
ON CONFLICT (external_id) DO UPDATE SET email = EXCLUDED.email, name = EXCLUDED.name
RETURNING *;

-- name: CreateUser :one
INSERT INTO app_user (external_id, email, name, is_admin, active)
VALUES (@external_id, sqlc.narg('email'), sqlc.narg('name'), @is_admin, @active)
RETURNING *;

-- name: ListUsers :many
SELECT * FROM app_user ORDER BY id LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: SetUserAdmin :one
UPDATE app_user SET is_admin = $2 WHERE id = $1 RETURNING *;

-- name: ListUserGroups :many
SELECT * FROM user_group ORDER BY name;

-- name: UpsertSSOGroup :one
INSERT INTO user_group (name, external_id) VALUES (@name, @external_id::text)
ON CONFLICT (external_id) DO UPDATE SET external_id = EXCLUDED.external_id
RETURNING *;

-- name: CreateUserGroup :one
INSERT INTO user_group (name, description) VALUES (@name, sqlc.narg('description')) RETURNING *;

-- name: AddGroupMember :exec
INSERT INTO user_group_member (group_id, user_id, source) VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: RemoveSSOMemberships :exec
DELETE FROM user_group_member
WHERE user_id = $1 AND source = 'sso' AND NOT (group_id = ANY(@keep::text[]));

-- name: ListUserGroupIDs :many
SELECT group_id FROM user_group_member WHERE user_id = $1 ORDER BY group_id;

-- name: ScopeLevel :one
SELECT level FROM permission_grant
WHERE scope = $1 AND scope_id = $2
  AND (user_id = ANY(@principals::text[]) OR group_id = ANY(@principals::text[]))
  AND (expires_at IS NULL OR expires_at > now())
ORDER BY level DESC LIMIT 1;

-- name: ProgramSeenViaProject :one
SELECT EXISTS (
    SELECT 1 FROM project p
    JOIN permission_grant g ON g.scope = 'project' AND g.scope_id = p.id
    WHERE p.program_id = $1
      AND (g.user_id = ANY(@principals::text[]) OR g.group_id = ANY(@principals::text[]))
      AND (g.expires_at IS NULL OR g.expires_at > now())
);



-- name: CreateGrant :one
INSERT INTO permission_grant (user_id, group_id, scope, scope_id, level, granted_by, expires_at)
VALUES (sqlc.narg('user_id'), sqlc.narg('group_id'), @scope, @scope_id, @level, sqlc.narg('granted_by'), sqlc.narg('expires_at'))
RETURNING *;

-- name: GetGrant :one
SELECT * FROM permission_grant WHERE id = $1;

-- name: DeleteGrant :execrows
DELETE FROM permission_grant WHERE id = $1;

-- name: ListGrants :many
SELECT * FROM permission_grant WHERE scope = $1 AND scope_id = $2 ORDER BY id;

-- name: DeleteExpiredGrants :many
DELETE FROM permission_grant WHERE expires_at <= now() RETURNING scope, scope_id;

-- name: AssetIDsInScope :many
SELECT a.id FROM asset a WHERE @scope::grant_scope = 'asset' AND a.id = @scope_id::text
UNION
SELECT ap.asset_id FROM asset_project ap WHERE @scope::grant_scope = 'project' AND ap.project_id = @scope_id::text
UNION
SELECT ap.asset_id FROM asset_project ap JOIN project p ON p.id = ap.project_id
WHERE @scope::grant_scope = 'program' AND p.program_id = @scope_id::text;

-- name: DeleteAssetAccess :exec
DELETE FROM asset_access WHERE asset_id = ANY(@asset_ids::text[]);

-- name: InsertAssetAccess :exec
INSERT INTO asset_access (principal_id, asset_id, level)
SELECT s.principal_id, s.asset_id, max(s.level)
FROM (
    SELECT coalesce(g.user_id, g.group_id) AS principal_id, g.scope_id AS asset_id, g.level
    FROM permission_grant g
    WHERE g.scope = 'asset' AND g.scope_id = ANY(@asset_ids::text[])
      AND (g.expires_at IS NULL OR g.expires_at > now())
    UNION ALL
    SELECT coalesce(g.user_id, g.group_id), ap.asset_id, g.level
    FROM asset_project ap
    JOIN permission_grant g ON g.scope = 'project' AND g.scope_id = ap.project_id
    WHERE ap.asset_id = ANY(@asset_ids::text[])
      AND (g.expires_at IS NULL OR g.expires_at > now())
    UNION ALL
    SELECT coalesce(g.user_id, g.group_id), ap.asset_id, g.level
    FROM asset_project ap
    JOIN project p ON p.id = ap.project_id
    JOIN permission_grant g ON g.scope = 'program' AND g.scope_id = p.program_id
    WHERE ap.asset_id = ANY(@asset_ids::text[])
      AND (g.expires_at IS NULL OR g.expires_at > now())
) s
GROUP BY s.principal_id, s.asset_id;

-- name: AssetLevels :many
SELECT asset_id, max(level)::permission_level AS level FROM asset_access
WHERE asset_id = ANY(@asset_ids::text[]) AND principal_id = ANY(@principals::text[])
GROUP BY asset_id;

-- name: FileLevels :many
SELECT rf.file_id, max(aa.level)::permission_level AS level FROM rendition_file rf
JOIN rendition r ON r.id = rf.rendition_id
JOIN asset_version v ON v.id = r.version_id
JOIN asset_access aa ON aa.asset_id = v.asset_id
WHERE rf.file_id = ANY(@file_ids::text[]) AND aa.principal_id = ANY(@principals::text[])
GROUP BY rf.file_id;
