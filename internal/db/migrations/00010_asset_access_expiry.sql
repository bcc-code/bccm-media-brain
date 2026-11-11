-- +goose Up
-- asset_access held one row per principal and asset with the highest level, and nothing about
-- when it ends, so expired access lasted until a sweep rebuilt the rows. It now holds one row
-- per level with the time that level ends (NULL: never), and every read compares it to the
-- clock. One row for the highest level would not do: when an EDIT grant expires, a VIEW grant
-- that does not must still count.
TRUNCATE asset_access;
ALTER TABLE asset_access DROP CONSTRAINT asset_access_pkey;
ALTER TABLE asset_access ADD COLUMN expires_at timestamptz;
ALTER TABLE asset_access ADD PRIMARY KEY (principal_id, asset_id, level) INCLUDE (expires_at);

INSERT INTO asset_access (principal_id, asset_id, level, expires_at)
SELECT s.principal_id, s.asset_id, s.level,
       CASE WHEN bool_or(s.expires_at IS NULL) THEN NULL ELSE max(s.expires_at) END
FROM (
    SELECT coalesce(g.user_id, g.group_id) AS principal_id, g.scope_id AS asset_id, g.level, g.expires_at
    FROM permission_grant g
    WHERE g.scope = 'asset'
    UNION ALL
    SELECT coalesce(g.user_id, g.group_id), ap.asset_id, g.level, g.expires_at
    FROM asset_project ap
    JOIN permission_grant g ON g.scope = 'project' AND g.scope_id = ap.project_id
    UNION ALL
    SELECT coalesce(g.user_id, g.group_id), ap.asset_id, g.level, g.expires_at
    FROM asset_project ap
    JOIN project p ON p.id = ap.project_id
    JOIN permission_grant g ON g.scope = 'program' AND g.scope_id = p.program_id
) s
WHERE s.expires_at IS NULL OR s.expires_at > now()
GROUP BY s.principal_id, s.asset_id, s.level;

-- +goose Down
-- Back to one row per principal and asset: the highest level that has not expired.
CREATE TEMP TABLE asset_access_old ON COMMIT DROP AS
SELECT principal_id, asset_id, max(level) AS level FROM asset_access
WHERE expires_at IS NULL OR expires_at > now()
GROUP BY principal_id, asset_id;
TRUNCATE asset_access;
ALTER TABLE asset_access DROP CONSTRAINT asset_access_pkey;
ALTER TABLE asset_access DROP COLUMN expires_at;
ALTER TABLE asset_access ADD PRIMARY KEY (principal_id, asset_id) INCLUDE (level);
INSERT INTO asset_access (principal_id, asset_id, level) SELECT principal_id, asset_id, level FROM asset_access_old;
