-- +goose Up
-- Vidispine identifies files by SHA-1, so imported files carry that hash.
ALTER TYPE hash_algo ADD VALUE 'sha1';

-- +goose Down
-- Postgres cannot drop an enum value, so rebuild hash_algo without 'sha1'.
DELETE FROM file_hash WHERE algo = 'sha1';
ALTER TYPE hash_algo RENAME TO hash_algo_old;
CREATE TYPE hash_algo AS ENUM ('xxh3_128', 'blake3', 'sha256', 'md5');
ALTER TABLE file_hash ALTER COLUMN algo TYPE hash_algo USING algo::text::hash_algo;
DROP TYPE hash_algo_old;
