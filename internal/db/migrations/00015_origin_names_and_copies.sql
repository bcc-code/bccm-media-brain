-- +goose Up
-- "*_by" names a user everywhere else (created_by, granted_by, asset_tag.added_by). These two
-- hold an origin (folder or manual; rule or person), so they are named after their type.
ALTER TABLE asset_project      RENAME COLUMN linked_by TO link_origin;
ALTER TABLE asset_group_member RENAME COLUMN added_by  TO member_origin;

-- When a copy was registered. Rows that exist take the time from their id: an id made by
-- new_id() starts with its creation time in milliseconds, as 10 characters of base 32.
ALTER TABLE file_copy ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
UPDATE file_copy SET created_at = to_timestamp((
    SELECT sum((strpos('0123456789ABCDEFGHJKMNPQRSTVWXYZ', substr(id, 3 + i, 1)) - 1) * 32::numeric ^ (10 - i))
    FROM generate_series(1, 10) AS i
) / 1000.0);

-- subtitle_track.language is a copy of its rendition's language, and updateRendition changed
-- only the original. The copy now follows.
UPDATE subtitle_track t SET language = r.language
FROM rendition r
WHERE r.id = t.rendition_id AND t.language IS DISTINCT FROM r.language;

-- +goose StatementBegin
CREATE FUNCTION sync_subtitle_language() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE subtitle_track SET language = NEW.language WHERE rendition_id = NEW.id;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER rendition_subtitle_language AFTER UPDATE OF language ON rendition
    FOR EACH ROW WHEN (OLD.language IS DISTINCT FROM NEW.language)
    EXECUTE FUNCTION sync_subtitle_language();

-- +goose Down
DROP TRIGGER rendition_subtitle_language ON rendition;
DROP FUNCTION sync_subtitle_language();
ALTER TABLE file_copy DROP COLUMN created_at;
ALTER TABLE asset_group_member RENAME COLUMN member_origin TO added_by;
ALTER TABLE asset_project      RENAME COLUMN link_origin   TO linked_by;
