-- +goose Up
-- A counter of edits, so that an update can name the revision it was based on and be refused
-- when someone else changed the row in between. Only the row's own editable fields count:
-- links and children do not bump it.
ALTER TABLE program          ADD COLUMN revision integer NOT NULL DEFAULT 1;
ALTER TABLE project          ADD COLUMN revision integer NOT NULL DEFAULT 1;
ALTER TABLE asset            ADD COLUMN revision integer NOT NULL DEFAULT 1;
ALTER TABLE asset_version    ADD COLUMN revision integer NOT NULL DEFAULT 1;
ALTER TABLE rendition        ADD COLUMN revision integer NOT NULL DEFAULT 1;
ALTER TABLE storage_location ADD COLUMN revision integer NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE storage_location DROP COLUMN revision;
ALTER TABLE rendition        DROP COLUMN revision;
ALTER TABLE asset_version    DROP COLUMN revision;
ALTER TABLE asset            DROP COLUMN revision;
ALTER TABLE project          DROP COLUMN revision;
ALTER TABLE program          DROP COLUMN revision;
