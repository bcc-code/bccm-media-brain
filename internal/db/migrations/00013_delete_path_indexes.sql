-- +goose Up
-- Postgres does not index the referencing side of a foreign key. Deleting a file or a subtitle
-- track looks for the rows that point at it, once per deleted row: without these, each of those
-- lookups reads the whole table, and subtitle_track gets a row per edit.
CREATE INDEX subtitle_track_parent_id_idx      ON subtitle_track (parent_id)      WHERE parent_id IS NOT NULL;
CREATE INDEX subtitle_track_source_file_id_idx ON subtitle_track (source_file_id) WHERE source_file_id IS NOT NULL;
CREATE INDEX timeline_source_file_id_idx       ON timeline (source_file_id)       WHERE source_file_id IS NOT NULL;

-- +goose Down
DROP INDEX timeline_source_file_id_idx;
DROP INDEX subtitle_track_source_file_id_idx;
DROP INDEX subtitle_track_parent_id_idx;
