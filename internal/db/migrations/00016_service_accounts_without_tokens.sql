-- +goose Up
-- Service accounts sign in with a token of the identity provider, like people, so MediaBrain
-- no longer issues tokens of its own.
DROP TABLE service_token;

-- A service account is created under its username at the identity provider and named by it
-- afterwards. Its external id cannot hold the name uniquely any more: the first sign-in
-- replaces it with the subject of the token.
CREATE UNIQUE INDEX app_user_service_name_idx ON app_user (name) WHERE kind = 'service';

-- +goose Down
DROP INDEX app_user_service_name_idx;

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
