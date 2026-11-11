-- +goose Up
-- Machine clients are users of kind 'service'. They hold grants like anyone else and sign in
-- with a service token instead of the identity provider.
CREATE TYPE user_kind AS ENUM ('person', 'service');
ALTER TABLE app_user ADD COLUMN kind user_kind NOT NULL DEFAULT 'person';

-- Only the SHA-256 of a token is stored: the token itself is shown once, when it is created.
CREATE TABLE service_token (
    id           text        PRIMARY KEY DEFAULT new_id('TK') CHECK (is_id(id, 'TK')),
    user_id      text        NOT NULL REFERENCES app_user (id) ON DELETE CASCADE,
    name         text        NOT NULL CHECK (name <> ''),
    token_hash   bytea       NOT NULL UNIQUE,
    created_by   text        REFERENCES app_user (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    last_used_at timestamptz,
    revoked_at   timestamptz
);
CREATE INDEX service_token_user_id_idx ON service_token (user_id);

-- +goose Down
DROP TABLE service_token;
-- Without the kind column a service account would look like a person nobody can sign in as.
DELETE FROM app_user WHERE kind = 'service';
ALTER TABLE app_user DROP COLUMN kind;
DROP TYPE user_kind;
