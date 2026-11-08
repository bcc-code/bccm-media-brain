-- MediaBrain data model, first version.
-- Source: the "MediaBrain data model" canvas (Model + Schema pages).
--
-- Conventions
--   * Ids are prefixed ULIDs as text: two-letter table prefix, dash, 26-char Crockford base32 ULID,
--     e.g. AS-01JC3QZ8V5M2N4P6R8T0W2Y4A6. new_id(prefix) is the default; the app may supply its own.
--     Prefixes: SL storage_location, FI file, FC file_copy, PG program, PJ project, PF project_folder,
--     PE person, US app_user, UG user_group, AS asset, AV asset_version, RE rendition, AG asset_group,
--     CR asset_credit, RI rights, TG tag, ST subtitle_track, FL face_cluster, FD face_detection,
--     TL timeline, TT timeline_track, TC timeline_clip, TM timeline_marker, GR permission_grant,
--     SS saved_search, CO collection. subtitle_cue (bigint) and asset_type (smallint) keep integer ids.
--   * created_at / updated_at are timestamptz NOT NULL DEFAULT now(); updated_at is kept by a trigger.
--   * jsonb columns are NOT NULL DEFAULT '{}' unless NULL carries meaning.
--   * Owned children cascade on delete; references restrict. Deleting an asset never deletes files.
--   * Media times are integer milliseconds; timeline times are frames at the timeline's rate.
--
-- Requires the pgvector extension (image pgvector/pgvector:pg17 or equivalent) and pgcrypto (contrib).

-- +goose Up

CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Prefixed ULID: '<XX>-' + 10 chars of millisecond time + 16 chars of randomness (Crockford base32).
-- Sorts by creation time within a prefix.
-- +goose StatementBegin
CREATE FUNCTION new_id(prefix text) RETURNS text
LANGUAGE plpgsql VOLATILE AS $$
DECLARE
    enc  constant text := '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
    ts   bigint := floor(extract(epoch FROM clock_timestamp()) * 1000);
    rnd  bytea  := gen_random_bytes(10);
    t    text   := '';
    r    text   := '';
    p    text;
    v    bigint;
    half int;
    i    int;
BEGIN
    FOR i IN 1..10 LOOP
        t  := substr(enc, (ts % 32)::int + 1, 1) || t;
        ts := ts / 32;
    END LOOP;
    -- 80 random bits as two 40-bit halves of 8 chars each.
    FOR half IN 0..1 LOOP
        v := 0;
        FOR i IN 0..4 LOOP
            v := v * 256 + get_byte(rnd, half * 5 + i);
        END LOOP;
        p := '';
        FOR i IN 1..8 LOOP
            p := substr(enc, (v % 32)::int + 1, 1) || p;
            v := v / 32;
        END LOOP;
        r := r || p;
    END LOOP;
    RETURN prefix || '-' || t || r;
END;
$$;
-- +goose StatementEnd

CREATE FUNCTION is_id(id text, prefix text) RETURNS boolean
LANGUAGE sql IMMUTABLE PARALLEL SAFE
RETURN id ~ ('^' || prefix || '-[0-7][0-9A-HJKMNP-TV-Z]{25}$');

-- +goose StatementBegin
CREATE FUNCTION set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- Enum types
-- ---------------------------------------------------------------------------

CREATE TYPE project_status    AS ENUM ('planned', 'active', 'delivered', 'archived');
CREATE TYPE folder_role       AS ENUM ('working', 'archive');
CREATE TYPE link_role         AS ENUM ('source', 'deliverable', 'reference');
CREATE TYPE link_origin       AS ENUM ('folder', 'manual');
CREATE TYPE version_status    AS ENUM ('draft', 'review', 'approved', 'final');
CREATE TYPE rendition_kind    AS ENUM ('master', 'encode', 'dub', 'subtitle', 'audio', 'other');
CREATE TYPE file_role         AS ENUM ('video', 'audio', 'subtitle', 'sidecar', 'package');
CREATE TYPE relation_kind     AS ENUM ('derived_from', 'trailer_of', 'uses_footage_from', 'replaces');
CREATE TYPE group_kind        AS ENUM ('shoot', 'card', 'day', 'other');
CREATE TYPE group_origin      AS ENUM ('auto', 'manual');
CREATE TYPE member_origin     AS ENUM ('rule', 'person');
CREATE TYPE location_kind     AS ENUM ('nas', 'lto', 's3', 'local');
CREATE TYPE hash_algo         AS ENUM ('xxh3_128', 'blake3', 'sha256', 'md5');
CREATE TYPE copy_status       AS ENUM ('present', 'missing', 'mismatch');
CREATE TYPE person_kind       AS ENUM ('person', 'organisation');
CREATE TYPE tag_source        AS ENUM ('file', 'ai', 'person');
CREATE TYPE track_kind        AS ENUM ('subtitle', 'cc', 'transcript');
CREATE TYPE track_origin      AS ENUM ('import', 'asr', 'manual');
CREATE TYPE cluster_status    AS ENUM ('auto', 'confirmed');
CREATE TYPE timeline_origin   AS ENUM ('rca', 'fcpxml', 'otio', 'edl');
CREATE TYPE track_media       AS ENUM ('video', 'audio');
CREATE TYPE clip_kind         AS ENUM ('clip', 'gap');
CREATE TYPE membership_source AS ENUM ('sso', 'manual');
CREATE TYPE grant_scope       AS ENUM ('program', 'project', 'asset_group', 'asset');
-- Ordered: each level includes the ones before it, so levels compare with < and >.
CREATE TYPE permission_level  AS ENUM ('view', 'download_original', 'edit', 'delete', 'admin');
CREATE TYPE search_target     AS ENUM ('asset', 'project');

-- ---------------------------------------------------------------------------
-- Files and storage
-- ---------------------------------------------------------------------------

CREATE TABLE storage_location (
    id         text          PRIMARY KEY DEFAULT new_id('SL') CHECK (is_id(id, 'SL')),
    name       text          NOT NULL UNIQUE CHECK (name <> ''),
    kind       location_kind NOT NULL,
    base_uri   text          NOT NULL,
    online     boolean       NOT NULL DEFAULT true,
    created_at timestamptz   NOT NULL DEFAULT now()
);

-- Unique content. Identity comes from file_hash, not from a path.
CREATE TABLE file (
    id          text        PRIMARY KEY DEFAULT new_id('FI') CHECK (is_id(id, 'FI')),
    size_bytes  bigint      NOT NULL CHECK (size_bytes >= 0),
    mime_type   text,
    duration_ms bigint      CHECK (duration_ms >= 0),
    codec       text,
    width       int         CHECK (width > 0),
    height      int         CHECK (height > 0),
    probe       jsonb       NOT NULL DEFAULT '{}', -- raw ffprobe output
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE file_hash (
    file_id     text        NOT NULL REFERENCES file (id) ON DELETE CASCADE,
    algo        hash_algo   NOT NULL,
    value       bytea       NOT NULL,
    computed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (file_id, algo),
    UNIQUE (algo, value)
);

CREATE TABLE file_copy (
    id               text        PRIMARY KEY DEFAULT new_id('FC') CHECK (is_id(id, 'FC')),
    file_id          text        NOT NULL REFERENCES file (id) ON DELETE RESTRICT,
    location_id      text        NOT NULL REFERENCES storage_location (id) ON DELETE RESTRICT,
    path             text        NOT NULL CHECK (path <> ''), -- relative to storage_location.base_uri
    status           copy_status NOT NULL DEFAULT 'present',
    last_seen_at     timestamptz,
    last_verified_at timestamptz, -- last time the hash was re-checked
    UNIQUE (location_id, path)
);
CREATE INDEX file_copy_file_id_idx ON file_copy (file_id);

-- ---------------------------------------------------------------------------
-- Organisation
-- ---------------------------------------------------------------------------

CREATE TABLE program (
    id          text        PRIMARY KEY DEFAULT new_id('PG') CHECK (is_id(id, 'PG')),
    code        text        NOT NULL UNIQUE CHECK (code ~ '^[A-Z]{4,5}$'),
    name        text        NOT NULL CHECK (name <> ''),
    description text,
    metadata    jsonb       NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER program_updated_at BEFORE UPDATE ON program
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- code starts with the program's code (e.g. JOSH_S01_E03 under JOSH); the prefix match is checked in the app.
CREATE TABLE project (
    id          text           PRIMARY KEY DEFAULT new_id('PJ') CHECK (is_id(id, 'PJ')),
    program_id  text           NOT NULL REFERENCES program (id) ON DELETE RESTRICT,
    code        text           NOT NULL UNIQUE CHECK (code ~ '^[A-Z]{4,5}_[A-Za-z0-9_]+$'),
    name        text           NOT NULL CHECK (name <> ''),
    status      project_status NOT NULL DEFAULT 'planned',
    starts_on   date,
    ends_on     date,
    metadata    jsonb          NOT NULL DEFAULT '{}',
    created_at  timestamptz    NOT NULL DEFAULT now(),
    updated_at  timestamptz    NOT NULL DEFAULT now(),
    CHECK (ends_on IS NULL OR starts_on IS NULL OR ends_on >= starts_on)
);
CREATE INDEX project_program_id_idx ON project (program_id);
CREATE TRIGGER project_updated_at BEFORE UPDATE ON project
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE project_folder (
    id          text        PRIMARY KEY DEFAULT new_id('PF') CHECK (is_id(id, 'PF')),
    project_id  text        NOT NULL REFERENCES project (id) ON DELETE CASCADE,
    location_id text        NOT NULL REFERENCES storage_location (id) ON DELETE RESTRICT,
    path        text        NOT NULL CHECK (path <> ''),
    role        folder_role NOT NULL DEFAULT 'working',
    UNIQUE (location_id, path)
);
CREATE INDEX project_folder_project_id_idx ON project_folder (project_id);

-- ---------------------------------------------------------------------------
-- People and users (referenced by the asset tables below)
-- ---------------------------------------------------------------------------

CREATE TABLE person (
    id           text        PRIMARY KEY DEFAULT new_id('PE') CHECK (is_id(id, 'PE')),
    name         text        NOT NULL CHECK (name <> ''),
    kind         person_kind NOT NULL DEFAULT 'person',
    external_ids jsonb       NOT NULL DEFAULT '{}', -- e.g. IMDb, internal ids
    notes        text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER person_updated_at BEFORE UPDATE ON person
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE app_user (
    id          text        PRIMARY KEY DEFAULT new_id('US') CHECK (is_id(id, 'US')),
    external_id text        NOT NULL UNIQUE, -- SSO subject
    email       text        UNIQUE,
    name        text,
    person_id   text        REFERENCES person (id) ON DELETE SET NULL,
    active      boolean     NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX app_user_person_id_idx ON app_user (person_id);

CREATE TABLE user_group (
    id          text PRIMARY KEY DEFAULT new_id('UG') CHECK (is_id(id, 'UG')),
    name        text NOT NULL UNIQUE CHECK (name <> ''),
    external_id text UNIQUE, -- SSO group, NULL for local groups
    description text
);

CREATE TABLE user_group_member (
    group_id text              NOT NULL REFERENCES user_group (id) ON DELETE CASCADE,
    user_id  text              NOT NULL REFERENCES app_user (id) ON DELETE CASCADE,
    source   membership_source NOT NULL DEFAULT 'manual',
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX user_group_member_user_id_idx ON user_group_member (user_id);

-- ---------------------------------------------------------------------------
-- Assets
-- ---------------------------------------------------------------------------

-- Lookup table, not an enum, so new types need no migration.
CREATE TABLE asset_type (
    id          smallint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        text     NOT NULL UNIQUE CHECK (name <> ''),
    description text
);
INSERT INTO asset_type (name, description) VALUES
    ('Raw material',   'Camera clips and other source recordings'),
    ('Deliverable',    'Finished output delivered to a channel or client'),
    ('Live recording', 'Recording of a live broadcast or event');

-- The logical item. A camera clip is one asset.
CREATE TABLE asset (
    id          text        PRIMARY KEY DEFAULT new_id('AS') CHECK (is_id(id, 'AS')),
    type_id     smallint    NOT NULL REFERENCES asset_type (id) ON DELETE RESTRICT,
    title       text        NOT NULL CHECK (title <> ''),
    description text,
    captured_at timestamptz, -- from camera metadata
    camera      text,        -- make, model or serial
    reel        text,        -- card or reel name
    metadata    jsonb       NOT NULL DEFAULT '{}',
    legacy_ids  text[]      NOT NULL DEFAULT '{}', -- ids from earlier systems, looked up with @>
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX asset_type_id_idx ON asset (type_id);
CREATE INDEX asset_legacy_ids_idx ON asset USING gin (legacy_ids);
CREATE INDEX asset_camera_captured_at_idx ON asset (camera, captured_at);
CREATE TRIGGER asset_updated_at BEFORE UPDATE ON asset
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE asset_project (
    asset_id   text        NOT NULL REFERENCES asset (id) ON DELETE CASCADE,
    project_id text        NOT NULL REFERENCES project (id) ON DELETE CASCADE,
    role       link_role   NOT NULL,
    linked_by  link_origin NOT NULL DEFAULT 'manual',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_id, project_id, role)
);
CREATE INDEX asset_project_project_id_idx ON asset_project (project_id);

-- Ordered edits of an asset: Rough cut, Review 1, Final.
CREATE TABLE asset_version (
    id         text           PRIMARY KEY DEFAULT new_id('AV') CHECK (is_id(id, 'AV')),
    asset_id   text           NOT NULL REFERENCES asset (id) ON DELETE CASCADE,
    number     int            NOT NULL CHECK (number > 0),
    label      text,
    status     version_status NOT NULL DEFAULT 'draft',
    created_by text           REFERENCES app_user (id) ON DELETE SET NULL,
    created_at timestamptz    NOT NULL DEFAULT now(),
    UNIQUE (asset_id, number)
);

-- One concrete form of a version. Dubs and subtitles belong to one version.
CREATE TABLE rendition (
    id         text           PRIMARY KEY DEFAULT new_id('RE') CHECK (is_id(id, 'RE')),
    version_id text           NOT NULL REFERENCES asset_version (id) ON DELETE CASCADE,
    kind       rendition_kind NOT NULL,
    language   text,                         -- BCP 47, e.g. no-NB
    purpose    text,
    overrides  jsonb          NOT NULL DEFAULT '{}', -- per-field metadata overrides (title, synopsis, ...)
    metadata   jsonb          NOT NULL DEFAULT '{}',
    created_at timestamptz    NOT NULL DEFAULT now()
);
CREATE INDEX rendition_version_id_idx ON rendition (version_id);

CREATE TABLE rendition_file (
    rendition_id text      NOT NULL REFERENCES rendition (id) ON DELETE CASCADE,
    file_id      text      NOT NULL REFERENCES file (id) ON DELETE RESTRICT,
    role         file_role NOT NULL,
    position     int       NOT NULL DEFAULT 0, -- order within the role
    PRIMARY KEY (rendition_id, file_id, role)
);
CREATE INDEX rendition_file_file_id_idx ON rendition_file (file_id);

CREATE TABLE asset_relation (
    from_asset_id text          NOT NULL REFERENCES asset (id) ON DELETE CASCADE,
    to_asset_id   text          NOT NULL REFERENCES asset (id) ON DELETE CASCADE,
    kind          relation_kind NOT NULL,
    created_at    timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (from_asset_id, to_asset_id, kind),
    CHECK (from_asset_id <> to_asset_id)
);
CREATE INDEX asset_relation_to_asset_id_idx ON asset_relation (to_asset_id);

-- Shoots, cards, days of clips; built automatically from capture metadata or by hand.
CREATE TABLE asset_group (
    id         text         PRIMARY KEY DEFAULT new_id('AG') CHECK (is_id(id, 'AG')),
    kind       group_kind   NOT NULL,
    name       text         NOT NULL CHECK (name <> ''),
    origin     group_origin NOT NULL DEFAULT 'manual',
    rule       jsonb        NOT NULL DEFAULT '{}', -- match criteria for auto groups
    project_id text         REFERENCES project (id) ON DELETE SET NULL,
    created_at timestamptz  NOT NULL DEFAULT now()
);
CREATE INDEX asset_group_project_id_idx ON asset_group (project_id);

CREATE TABLE asset_group_member (
    group_id text          NOT NULL REFERENCES asset_group (id) ON DELETE CASCADE,
    asset_id text          NOT NULL REFERENCES asset (id) ON DELETE CASCADE,
    added_by member_origin NOT NULL DEFAULT 'person',
    added_at timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, asset_id)
);
CREATE INDEX asset_group_member_asset_id_idx ON asset_group_member (asset_id);

-- ---------------------------------------------------------------------------
-- Descriptive metadata (per asset; a row with rendition_id overrides it for that rendition)
-- ---------------------------------------------------------------------------

CREATE TABLE asset_credit (
    id             text PRIMARY KEY DEFAULT new_id('CR') CHECK (is_id(id, 'CR')),
    asset_id       text NOT NULL REFERENCES asset (id) ON DELETE CASCADE,
    rendition_id   text REFERENCES rendition (id) ON DELETE CASCADE,
    person_id      text NOT NULL REFERENCES person (id) ON DELETE RESTRICT,
    role           text NOT NULL CHECK (role <> ''), -- cast, director, voice, ...
    billing_order  int,
    character_name text
);
CREATE INDEX asset_credit_asset_id_idx ON asset_credit (asset_id);
CREATE INDEX asset_credit_rendition_id_idx ON asset_credit (rendition_id);
CREATE INDEX asset_credit_person_id_idx ON asset_credit (person_id);

CREATE TABLE rights (
    id           text   PRIMARY KEY DEFAULT new_id('RI') CHECK (is_id(id, 'RI')),
    asset_id     text   NOT NULL REFERENCES asset (id) ON DELETE CASCADE,
    rendition_id text   REFERENCES rendition (id) ON DELETE CASCADE,
    holder_id    text   REFERENCES person (id) ON DELETE RESTRICT,
    license_type text,
    territories  text[] NOT NULL DEFAULT '{}', -- ISO 3166 codes
    valid_from   date,
    valid_to     date,
    notes        text,
    CHECK (valid_to IS NULL OR valid_from IS NULL OR valid_to >= valid_from)
);
CREATE INDEX rights_asset_id_idx ON rights (asset_id);
CREATE INDEX rights_rendition_id_idx ON rights (rendition_id);
CREATE INDEX rights_holder_id_idx ON rights (holder_id);

CREATE TABLE tag (
    id        text PRIMARY KEY DEFAULT new_id('TG') CHECK (is_id(id, 'TG')),
    namespace text NOT NULL DEFAULT '', -- e.g. topic, location; '' for flat tags
    name      text NOT NULL CHECK (name <> ''),
    UNIQUE (namespace, name)
);

CREATE TABLE asset_tag (
    asset_id   text        NOT NULL REFERENCES asset (id) ON DELETE CASCADE,
    tag_id     text        NOT NULL REFERENCES tag (id) ON DELETE CASCADE,
    source     tag_source  NOT NULL,
    confidence real        CHECK (confidence BETWEEN 0 AND 1),
    added_by   text        REFERENCES app_user (id) ON DELETE SET NULL,
    added_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_id, tag_id, source),
    CHECK (source <> 'ai' OR confidence IS NOT NULL)
);
CREATE INDEX asset_tag_tag_id_idx ON asset_tag (tag_id);

-- ---------------------------------------------------------------------------
-- Text and vision
-- ---------------------------------------------------------------------------

-- Imported subtitles. Each edit creates a new revision pointing at its parent.
CREATE TABLE subtitle_track (
    id             text         PRIMARY KEY DEFAULT new_id('ST') CHECK (is_id(id, 'ST')),
    rendition_id   text         NOT NULL REFERENCES rendition (id) ON DELETE CASCADE,
    asset_id       text         NOT NULL REFERENCES asset (id) ON DELETE CASCADE, -- copy, for permission filters
    source_file_id text         REFERENCES file (id) ON DELETE SET NULL,         -- the imported file
    language       text,                                                          -- BCP 47
    kind           track_kind   NOT NULL DEFAULT 'subtitle',
    origin         track_origin NOT NULL DEFAULT 'import',
    revision       int          NOT NULL DEFAULT 1 CHECK (revision > 0),
    parent_id      text         REFERENCES subtitle_track (id) ON DELETE SET NULL, -- previous revision
    created_by     text         REFERENCES app_user (id) ON DELETE SET NULL,
    created_at     timestamptz  NOT NULL DEFAULT now(),
    UNIQUE (rendition_id, revision)
);
CREATE INDEX subtitle_track_asset_id_idx ON subtitle_track (asset_id);

CREATE TABLE subtitle_cue (
    id       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    track_id text   NOT NULL REFERENCES subtitle_track (id) ON DELETE CASCADE,
    asset_id text   NOT NULL REFERENCES asset (id) ON DELETE CASCADE, -- copy, for permission filters
    seq      int    NOT NULL,
    start_ms int    NOT NULL CHECK (start_ms >= 0),
    end_ms   int    NOT NULL,
    text     text   NOT NULL,
    speaker  text,
    tsv      tsvector GENERATED ALWAYS AS (to_tsvector('simple', text)) STORED,
    UNIQUE (track_id, seq),
    CHECK (end_ms > start_ms)
);
CREATE INDEX subtitle_cue_asset_id_idx ON subtitle_cue (asset_id);
CREATE INDEX subtitle_cue_tsv_idx ON subtitle_cue USING gin (tsv);

-- One row per cue per model. The dimension depends on the model, so the column has none;
-- add one HNSW index per model when a model is chosen, e.g.:
--   CREATE INDEX ON cue_embedding USING hnsw ((embedding::vector(768)) vector_cosine_ops)
--       WHERE model = '<model name>';
CREATE TABLE cue_embedding (
    cue_id    bigint NOT NULL REFERENCES subtitle_cue (id) ON DELETE CASCADE,
    model     text   NOT NULL,
    asset_id  text   NOT NULL REFERENCES asset (id) ON DELETE CASCADE, -- copy, for permission filters
    embedding vector NOT NULL,
    PRIMARY KEY (cue_id, model)
);
CREATE INDEX cue_embedding_asset_id_idx ON cue_embedding (asset_id);

CREATE TABLE face_cluster (
    id         text           PRIMARY KEY DEFAULT new_id('FL') CHECK (is_id(id, 'FL')),
    person_id  text           REFERENCES person (id) ON DELETE SET NULL, -- set when named
    centroid   vector(512),
    status     cluster_status NOT NULL DEFAULT 'auto',
    updated_at timestamptz    NOT NULL DEFAULT now()
);
CREATE INDEX face_cluster_person_id_idx ON face_cluster (person_id);
CREATE TRIGGER face_cluster_updated_at BEFORE UPDATE ON face_cluster
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE face_detection (
    id         text        PRIMARY KEY DEFAULT new_id('FD') CHECK (is_id(id, 'FD')),
    file_id    text        NOT NULL REFERENCES file (id) ON DELETE CASCADE,
    start_ms   int         NOT NULL CHECK (start_ms >= 0),
    end_ms     int         NOT NULL,
    bbox       jsonb       NOT NULL, -- {x, y, w, h} as fractions 0-1 of the frame
    embedding  vector(512) NOT NULL,
    confidence real        CHECK (confidence BETWEEN 0 AND 1),
    cluster_id text        REFERENCES face_cluster (id) ON DELETE SET NULL,
    CHECK (end_ms >= start_ms)
);
CREATE INDEX face_detection_file_id_idx ON face_detection (file_id);
CREATE INDEX face_detection_cluster_id_idx ON face_detection (cluster_id);
CREATE INDEX face_detection_embedding_idx ON face_detection USING hnsw (embedding vector_cosine_ops);

-- ---------------------------------------------------------------------------
-- Timelines (RCA, FCPXML import, Resolve via FCPXML or OTIO)
-- ---------------------------------------------------------------------------

CREATE TABLE timeline (
    id             text            PRIMARY KEY DEFAULT new_id('TL') CHECK (is_id(id, 'TL')),
    project_id     text            NOT NULL REFERENCES project (id) ON DELETE CASCADE,
    version_id     text            REFERENCES asset_version (id) ON DELETE SET NULL, -- the cut it builds
    name           text            NOT NULL CHECK (name <> ''),
    rate_num       int             NOT NULL CHECK (rate_num > 0), -- frame rate, e.g. 25/1 or 30000/1001
    rate_den       int             NOT NULL DEFAULT 1 CHECK (rate_den > 0),
    start_frame    bigint          NOT NULL DEFAULT 0,            -- start timecode in frames
    origin         timeline_origin NOT NULL DEFAULT 'rca',
    source_file_id text            REFERENCES file (id) ON DELETE SET NULL, -- imported file
    otio           jsonb           NOT NULL DEFAULT '{}',          -- original as OTIO, for export
    created_at     timestamptz     NOT NULL DEFAULT now(),
    updated_at     timestamptz     NOT NULL DEFAULT now()
);
CREATE INDEX timeline_project_id_idx ON timeline (project_id);
CREATE INDEX timeline_version_id_idx ON timeline (version_id);
CREATE TRIGGER timeline_updated_at BEFORE UPDATE ON timeline
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE timeline_track (
    id          text        PRIMARY KEY DEFAULT new_id('TT') CHECK (is_id(id, 'TT')),
    timeline_id text        NOT NULL REFERENCES timeline (id) ON DELETE CASCADE,
    kind        track_media NOT NULL,
    position    int         NOT NULL,
    name        text,
    enabled     boolean     NOT NULL DEFAULT true,
    UNIQUE (timeline_id, kind, position)
);

CREATE TABLE timeline_clip (
    id         text      PRIMARY KEY DEFAULT new_id('TC') CHECK (is_id(id, 'TC')),
    track_id   text      NOT NULL REFERENCES timeline_track (id) ON DELETE CASCADE,
    kind       clip_kind NOT NULL DEFAULT 'clip',
    asset_id   text      REFERENCES asset (id) ON DELETE RESTRICT,
    file_id    text      REFERENCES file (id) ON DELETE RESTRICT,
    source_in  bigint    NOT NULL DEFAULT 0, -- frames at the timeline's rate
    source_out bigint    NOT NULL,
    record_in  bigint    NOT NULL,           -- frames
    name       text,
    unresolved jsonb,                        -- original media reference when nothing matched; NULL once resolved
    CHECK (source_out > source_in),
    CHECK (kind = 'gap' OR asset_id IS NOT NULL OR file_id IS NOT NULL OR unresolved IS NOT NULL)
);
CREATE INDEX timeline_clip_track_id_record_in_idx ON timeline_clip (track_id, record_in);
CREATE INDEX timeline_clip_asset_id_idx ON timeline_clip (asset_id);
CREATE INDEX timeline_clip_file_id_idx ON timeline_clip (file_id);

CREATE TABLE timeline_marker (
    id              text   PRIMARY KEY DEFAULT new_id('TM') CHECK (is_id(id, 'TM')),
    timeline_id     text   NOT NULL REFERENCES timeline (id) ON DELETE CASCADE,
    at_frame        bigint NOT NULL,
    duration_frames bigint NOT NULL DEFAULT 0 CHECK (duration_frames >= 0),
    label           text,
    color           text,
    note            text
);
CREATE INDEX timeline_marker_timeline_id_at_frame_idx ON timeline_marker (timeline_id, at_frame);

-- ---------------------------------------------------------------------------
-- Access
-- ---------------------------------------------------------------------------

-- Grants are additive only. scope_id points into the table named by scope (its prefix matches), so it has no FK.
CREATE TABLE permission_grant (
    id         text             PRIMARY KEY DEFAULT new_id('GR') CHECK (is_id(id, 'GR')),
    user_id    text             REFERENCES app_user (id) ON DELETE CASCADE,
    group_id   text             REFERENCES user_group (id) ON DELETE CASCADE,
    scope      grant_scope      NOT NULL,
    scope_id   text             NOT NULL,
    level      permission_level NOT NULL,
    granted_by text             REFERENCES app_user (id) ON DELETE SET NULL,
    granted_at timestamptz      NOT NULL DEFAULT now(),
    expires_at timestamptz,
    CHECK (num_nonnulls(user_id, group_id) = 1)
);
CREATE INDEX permission_grant_scope_idx ON permission_grant (scope, scope_id);
CREATE INDEX permission_grant_user_id_idx ON permission_grant (user_id);
CREATE INDEX permission_grant_group_id_idx ON permission_grant (group_id);

-- Derived permission index: the highest level each user or group has on each asset.
-- Rebuilt by a job when grants, group memberships or project/group links change.
-- Every search joins it on principal_id IN (user id, the user's group ids).
CREATE TABLE asset_access (
    principal_id text             NOT NULL, -- app_user.id (US-) or user_group.id (UG-)
    asset_id     text             NOT NULL REFERENCES asset (id) ON DELETE CASCADE,
    level        permission_level NOT NULL,
    refreshed_at timestamptz      NOT NULL DEFAULT now(),
    PRIMARY KEY (principal_id, asset_id) INCLUDE (level)
);
CREATE INDEX asset_access_asset_id_idx ON asset_access (asset_id);

-- ---------------------------------------------------------------------------
-- Saved searches and collections
-- ---------------------------------------------------------------------------

CREATE TABLE saved_search (
    id            text          PRIMARY KEY DEFAULT new_id('SS') CHECK (is_id(id, 'SS')),
    owner_id      text          NOT NULL REFERENCES app_user (id) ON DELETE CASCADE,
    name          text          NOT NULL CHECK (name <> ''),
    target        search_target NOT NULL DEFAULT 'asset',
    query         text,                  -- full-text words
    type_id       smallint      REFERENCES asset_type (id) ON DELETE SET NULL,
    project_id    text          REFERENCES project (id) ON DELETE SET NULL,
    tag_ids       text[]        NOT NULL DEFAULT '{}',
    captured_from timestamptz,
    captured_to   timestamptz,
    created_at    timestamptz   NOT NULL DEFAULT now()
);
CREATE INDEX saved_search_owner_id_idx ON saved_search (owner_id);

CREATE TABLE collection (
    id         text        PRIMARY KEY DEFAULT new_id('CO') CHECK (is_id(id, 'CO')),
    owner_id   text        NOT NULL REFERENCES app_user (id) ON DELETE CASCADE,
    name       text        NOT NULL CHECK (name <> ''),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX collection_owner_id_idx ON collection (owner_id);

CREATE TABLE collection_item (
    collection_id text NOT NULL REFERENCES collection (id) ON DELETE CASCADE,
    asset_id      text NOT NULL REFERENCES asset (id) ON DELETE CASCADE,
    position      int  NOT NULL DEFAULT 0,
    PRIMARY KEY (collection_id, asset_id)
);
CREATE INDEX collection_item_asset_id_idx ON collection_item (asset_id);

-- +goose Down

DROP TABLE collection_item;
DROP TABLE collection;
DROP TABLE saved_search;
DROP TABLE asset_access;
DROP TABLE permission_grant;
DROP TABLE timeline_marker;
DROP TABLE timeline_clip;
DROP TABLE timeline_track;
DROP TABLE timeline;
DROP TABLE face_detection;
DROP TABLE face_cluster;
DROP TABLE cue_embedding;
DROP TABLE subtitle_cue;
DROP TABLE subtitle_track;
DROP TABLE asset_tag;
DROP TABLE tag;
DROP TABLE rights;
DROP TABLE asset_credit;
DROP TABLE asset_group_member;
DROP TABLE asset_group;
DROP TABLE asset_relation;
DROP TABLE rendition_file;
DROP TABLE rendition;
DROP TABLE asset_version;
DROP TABLE asset_project;
DROP TABLE asset;
DROP TABLE asset_type;
DROP TABLE user_group_member;
DROP TABLE user_group;
DROP TABLE app_user;
DROP TABLE person;
DROP TABLE project_folder;
DROP TABLE project;
DROP TABLE program;
DROP TABLE file_copy;
DROP TABLE file_hash;
DROP TABLE file;
DROP TABLE storage_location;

DROP TYPE search_target;
DROP TYPE permission_level;
DROP TYPE grant_scope;
DROP TYPE membership_source;
DROP TYPE clip_kind;
DROP TYPE track_media;
DROP TYPE timeline_origin;
DROP TYPE cluster_status;
DROP TYPE track_origin;
DROP TYPE track_kind;
DROP TYPE tag_source;
DROP TYPE person_kind;
DROP TYPE copy_status;
DROP TYPE hash_algo;
DROP TYPE location_kind;
DROP TYPE member_origin;
DROP TYPE group_origin;
DROP TYPE group_kind;
DROP TYPE relation_kind;
DROP TYPE file_role;
DROP TYPE rendition_kind;
DROP TYPE version_status;
DROP TYPE link_origin;
DROP TYPE link_role;
DROP TYPE folder_role;
DROP TYPE project_status;

DROP FUNCTION is_id(text, text);
DROP FUNCTION new_id(text);
DROP FUNCTION set_updated_at();
DROP EXTENSION IF EXISTS pgcrypto;
DROP EXTENSION IF EXISTS vector;
