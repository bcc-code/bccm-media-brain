-- +goose Up
-- A user is looked up by lower(email), so the address must be unique that way too: with the
-- plain UNIQUE, 'Ann@x.org' and 'ann@x.org' were two accounts and a grant by email reached
-- whichever the lookup returned first. The index also serves that lookup.

-- Where two accounts already share an address, the older one keeps it: the same rule sign-in
-- applies to a token that brings an address another account has.
UPDATE app_user u SET email = NULL
WHERE EXISTS (
    SELECT 1 FROM app_user o
    WHERE lower(o.email) = lower(u.email) AND (o.created_at, o.id) < (u.created_at, u.id)
);

ALTER TABLE app_user DROP CONSTRAINT app_user_email_key;
CREATE UNIQUE INDEX app_user_email_lower_key ON app_user (lower(email));

-- +goose Down
-- The addresses cleared above are not restored: nothing records them.
DROP INDEX app_user_email_lower_key;
ALTER TABLE app_user ADD CONSTRAINT app_user_email_key UNIQUE (email);
