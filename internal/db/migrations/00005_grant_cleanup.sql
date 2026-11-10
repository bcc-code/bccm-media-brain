-- Remove grants when the program, project, asset group or asset they point at is deleted.
-- permission_grant.scope_id has no FK (it points into the table named by scope), so without
-- this a deleted object leaves its grants behind. 00004 did the same for collections, saved
-- searches and notes. Triggers fire for cascaded deletes too.

-- +goose Up

CREATE TRIGGER program_delete_grants AFTER DELETE ON program
    FOR EACH ROW EXECUTE FUNCTION delete_object_grants('program');
CREATE TRIGGER project_delete_grants AFTER DELETE ON project
    FOR EACH ROW EXECUTE FUNCTION delete_object_grants('project');
CREATE TRIGGER asset_group_delete_grants AFTER DELETE ON asset_group
    FOR EACH ROW EXECUTE FUNCTION delete_object_grants('asset_group');
CREATE TRIGGER asset_delete_grants AFTER DELETE ON asset
    FOR EACH ROW EXECUTE FUNCTION delete_object_grants('asset');

-- Grants already orphaned before this migration.
DELETE FROM permission_grant g
WHERE (g.scope = 'program'     AND NOT EXISTS (SELECT 1 FROM program     WHERE id = g.scope_id))
   OR (g.scope = 'project'     AND NOT EXISTS (SELECT 1 FROM project     WHERE id = g.scope_id))
   OR (g.scope = 'asset_group' AND NOT EXISTS (SELECT 1 FROM asset_group WHERE id = g.scope_id))
   OR (g.scope = 'asset'       AND NOT EXISTS (SELECT 1 FROM asset       WHERE id = g.scope_id));

-- +goose Down

DROP TRIGGER asset_delete_grants ON asset;
DROP TRIGGER asset_group_delete_grants ON asset_group;
DROP TRIGGER project_delete_grants ON project;
DROP TRIGGER program_delete_grants ON program;
