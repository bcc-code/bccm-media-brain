-- Notes on assets, and sharing of collections, saved searches and notes.
--
-- Sharing reuses permission_grant: a grant on a collection, saved search or note gives access to
-- that object only, never to the assets in it, so asset_access ignores these scopes.
-- Levels on them: view = open / read, edit = change, admin = share and delete. The owner or
-- author always has admin without a grant.
--
-- saved_search stores the search as sent (the API's SearchInput) in one jsonb column instead of
-- fixed columns, so new search filters need no migration.

-- +goose Up

ALTER TYPE grant_scope ADD VALUE 'collection';
ALTER TYPE grant_scope ADD VALUE 'saved_search';
ALTER TYPE grant_scope ADD VALUE 'note';

-- Markdown note on an asset. Private to the author until shared.
CREATE TABLE note (
    id         text        PRIMARY KEY DEFAULT new_id('NT') CHECK (is_id(id, 'NT')),
    asset_id   text        NOT NULL REFERENCES asset (id) ON DELETE CASCADE,
    author_id  text        NOT NULL REFERENCES app_user (id) ON DELETE CASCADE,
    body       text        NOT NULL CHECK (btrim(body) <> ''), -- markdown as written
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX note_asset_id_idx ON note (asset_id, created_at DESC);
CREATE INDEX note_author_id_idx ON note (author_id, created_at DESC);
CREATE TRIGGER note_updated_at BEFORE UPDATE ON note
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- grant scope_id has no FK, so remove the grants of a shared object when it is deleted
-- (also when a note goes with its asset). TG_ARGV[0] is the grant scope.
-- +goose StatementBegin
CREATE FUNCTION delete_object_grants() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM permission_grant WHERE scope = TG_ARGV[0]::grant_scope AND scope_id = OLD.id;
    RETURN OLD;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER collection_delete_grants AFTER DELETE ON collection
    FOR EACH ROW EXECUTE FUNCTION delete_object_grants('collection');
CREATE TRIGGER saved_search_delete_grants AFTER DELETE ON saved_search
    FOR EACH ROW EXECUTE FUNCTION delete_object_grants('saved_search');
CREATE TRIGGER note_delete_grants AFTER DELETE ON note
    FOR EACH ROW EXECUTE FUNCTION delete_object_grants('note');

-- Saved searches: one filter document instead of fixed columns.
ALTER TABLE saved_search
    DROP COLUMN query,
    DROP COLUMN type_id,
    DROP COLUMN project_id,
    DROP COLUMN tag_ids,
    DROP COLUMN captured_from,
    DROP COLUMN captured_to,
    ADD COLUMN filter jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(filter) = 'object'),
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
COMMENT ON COLUMN saved_search.filter IS 'The SearchInput as sent, camelCase keys. Validated by the app; ids in it may point to deleted rows.';
CREATE TRIGGER saved_search_updated_at BEFORE UPDATE ON saved_search
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down

DROP TRIGGER saved_search_updated_at ON saved_search;
ALTER TABLE saved_search
    DROP COLUMN updated_at,
    DROP COLUMN filter,
    ADD COLUMN query         text,
    ADD COLUMN type_id       smallint REFERENCES asset_type (id) ON DELETE SET NULL,
    ADD COLUMN project_id    text     REFERENCES project (id) ON DELETE SET NULL,
    ADD COLUMN tag_ids       text[]   NOT NULL DEFAULT '{}',
    ADD COLUMN captured_from timestamptz,
    ADD COLUMN captured_to   timestamptz;

DROP TRIGGER note_delete_grants ON note;
DROP TRIGGER saved_search_delete_grants ON saved_search;
DROP TRIGGER collection_delete_grants ON collection;
DROP FUNCTION delete_object_grants();
DROP TABLE note;

-- Postgres cannot drop an enum value, so rebuild grant_scope without the new ones.
DELETE FROM permission_grant WHERE scope::text IN ('collection', 'saved_search', 'note');
ALTER TYPE grant_scope RENAME TO grant_scope_old;
CREATE TYPE grant_scope AS ENUM ('program', 'project', 'asset_group', 'asset');
DROP INDEX permission_grant_scope_idx;
ALTER TABLE permission_grant ALTER COLUMN scope TYPE grant_scope USING scope::text::grant_scope;
CREATE INDEX permission_grant_scope_idx ON permission_grant (scope, scope_id);
DROP TYPE grant_scope_old;
