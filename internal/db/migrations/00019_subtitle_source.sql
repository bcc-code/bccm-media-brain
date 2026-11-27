-- +goose Up
-- Where a revision's text came from: the speech recognition model of an ASR run
-- ("nb-whisper-large") or the system it was taken from ("Mediabanken"). It was named model,
-- which did not fit text imported from another system.
ALTER TABLE subtitle_track RENAME COLUMN model TO source;
ALTER TABLE subtitle_track RENAME CONSTRAINT subtitle_track_model_check TO subtitle_track_source_check;

-- +goose Down
ALTER TABLE subtitle_track RENAME CONSTRAINT subtitle_track_source_check TO subtitle_track_model_check;
ALTER TABLE subtitle_track RENAME COLUMN source TO model;
