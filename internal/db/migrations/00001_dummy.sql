-- PLACEHOLDER table proving the migration -> sqlc -> GraphQL pipeline.
-- Replace with the real media data model.

-- +goose Up
CREATE TABLE dummy (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT        NOT NULL CHECK (name <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE dummy;
