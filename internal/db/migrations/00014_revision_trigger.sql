-- +goose Up
-- updated_at for the three revisioned tables that had none, so that all six can say when
-- they last changed. Rows that exist start at their creation time. This comes before the
-- triggers below: filling the column is not an edit.
ALTER TABLE asset_version    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE rendition        ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE storage_location ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
UPDATE asset_version    SET updated_at = created_at;
UPDATE rendition        SET updated_at = created_at;
UPDATE storage_location SET updated_at = created_at;

CREATE TRIGGER asset_version_updated_at BEFORE UPDATE ON asset_version
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER rendition_updated_at BEFORE UPDATE ON rendition
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER storage_location_updated_at BEFORE UPDATE ON storage_location
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The revision was incremented by each UPDATE statement that remembered to. One that forgot,
-- or a fix by hand, left the counter where it was and an update based on the old row was then
-- accepted. The database counts instead: every UPDATE of the row is one revision, whatever
-- the statement says. An UPDATE's own "WHERE revision = ..." still sees the old value.
-- +goose StatementBegin
CREATE FUNCTION bump_revision() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.revision = OLD.revision + 1;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER program_revision BEFORE UPDATE ON program
    FOR EACH ROW EXECUTE FUNCTION bump_revision();
CREATE TRIGGER project_revision BEFORE UPDATE ON project
    FOR EACH ROW EXECUTE FUNCTION bump_revision();
CREATE TRIGGER asset_revision BEFORE UPDATE ON asset
    FOR EACH ROW EXECUTE FUNCTION bump_revision();
CREATE TRIGGER asset_version_revision BEFORE UPDATE ON asset_version
    FOR EACH ROW EXECUTE FUNCTION bump_revision();
CREATE TRIGGER rendition_revision BEFORE UPDATE ON rendition
    FOR EACH ROW EXECUTE FUNCTION bump_revision();
CREATE TRIGGER storage_location_revision BEFORE UPDATE ON storage_location
    FOR EACH ROW EXECUTE FUNCTION bump_revision();

-- +goose Down
DROP TRIGGER storage_location_revision ON storage_location;
DROP TRIGGER rendition_revision ON rendition;
DROP TRIGGER asset_version_revision ON asset_version;
DROP TRIGGER asset_revision ON asset;
DROP TRIGGER project_revision ON project;
DROP TRIGGER program_revision ON program;
DROP FUNCTION bump_revision();

DROP TRIGGER storage_location_updated_at ON storage_location;
DROP TRIGGER rendition_updated_at ON rendition;
DROP TRIGGER asset_version_updated_at ON asset_version;
ALTER TABLE storage_location DROP COLUMN updated_at;
ALTER TABLE rendition        DROP COLUMN updated_at;
ALTER TABLE asset_version    DROP COLUMN updated_at;
