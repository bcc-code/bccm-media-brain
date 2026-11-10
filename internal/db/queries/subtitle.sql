-- name: CreateSubtitleTrack :one
INSERT INTO subtitle_track (rendition_id, asset_id, source_file_id, language, kind, origin, revision, parent_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING *;

-- name: GetSubtitleTrack :one
SELECT * FROM subtitle_track WHERE id = $1;

-- name: LatestSubtitleTrack :one
SELECT * FROM subtitle_track WHERE rendition_id = $1 ORDER BY revision DESC LIMIT 1;


-- name: ListAssetTracks :many
SELECT * FROM subtitle_track WHERE asset_id = $1 ORDER BY rendition_id, revision DESC;

-- name: DeleteSubtitleTrack :execrows
DELETE FROM subtitle_track WHERE id = $1;

-- name: InsertSubtitleCues :copyfrom
INSERT INTO subtitle_cue (track_id, asset_id, seq, start_ms, end_ms, text, speaker)
VALUES ($1, $2, $3, $4, $5, $6, $7);


-- name: ListRenditionTracks :many
SELECT * FROM subtitle_track WHERE rendition_id = ANY(@rendition_ids::text[]) ORDER BY rendition_id, revision DESC;

-- name: ListSubtitleCues :many
SELECT c.track_id, c.seq, c.start_ms, c.end_ms, c.text, c.speaker
FROM (
    SELECT sc.track_id, sc.seq, sc.start_ms, sc.end_ms, sc.text, sc.speaker,
           row_number() OVER (PARTITION BY sc.track_id ORDER BY sc.seq) AS rn
    FROM subtitle_cue sc WHERE sc.track_id = ANY(@track_ids::text[])
) c
WHERE c.rn > sqlc.arg('offset')::int AND c.rn <= sqlc.arg('offset')::int + sqlc.arg('limit')::int
ORDER BY c.track_id, c.seq;
