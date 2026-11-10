-- name: ListLanguages :many
SELECT * FROM language WHERE (NOT @active_only::bool OR active) ORDER BY name;

-- name: GetLanguage :one
SELECT * FROM language WHERE code = $1;
