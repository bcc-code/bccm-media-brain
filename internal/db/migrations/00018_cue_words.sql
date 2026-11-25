-- +goose Up
-- Word timing of a cue, as speech recognition reports it:
-- [{"startMs": 1200, "endMs": 1500, "text": "Hei", "confidence": 0.93}, ...].
-- NULL when the source had none (SRT, WebVTT, most edits).
ALTER TABLE subtitle_cue ADD COLUMN words jsonb CHECK (jsonb_typeof(words) = 'array');

-- +goose Down
ALTER TABLE subtitle_cue DROP COLUMN words;
