-- +goose Up
-- System administrators bypass grant checks and manage programs, storage and files.
ALTER TABLE app_user ADD COLUMN is_admin boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE app_user DROP COLUMN is_admin;
