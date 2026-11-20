-- +goose Up
-- The model that produced a revision's text, such as the speech recognition model of an ASR
-- import. Each revision keeps its own: an edit is MANUAL and has none.
ALTER TABLE subtitle_track ADD COLUMN model text CHECK (model <> '');

-- +goose Down
ALTER TABLE subtitle_track DROP COLUMN model;
