-- +goose Up
DROP TABLE dummy;

-- +goose Down
CREATE TABLE dummy (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
