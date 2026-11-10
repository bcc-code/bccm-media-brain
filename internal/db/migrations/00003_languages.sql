-- Languages as a concept of their own: spoken, signed, and the special codes
-- zxx (no linguistic content), und (undetermined) and mul (multiple languages).
--
-- Keyed by the BCP 47 tag (nb, en-GB, nsl, zxx), like asset_type keeps an integer id:
-- the code is stable, readable and what files and APIs already use.
-- Sign languages use their ISO 639-3 code (nsl Norwegian, ase American, bfi British).

-- +goose Up

CREATE TYPE language_modality AS ENUM ('spoken', 'signed', 'none', 'special');

CREATE TABLE language (
    code        text              PRIMARY KEY CHECK (code ~ '^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$'),
    name        text              NOT NULL CHECK (name <> ''), -- English name
    native_name text,                                          -- name in the language itself
    modality    language_modality NOT NULL DEFAULT 'spoken',
    base_code   text              REFERENCES language (code) ON DELETE RESTRICT, -- pt for pt-BR
    -- Other codes seen in files, e.g. ISO 639-2 'nor' or 'ger' from ffprobe or subtitle headers.
    alt_codes   text[]            NOT NULL DEFAULT '{}',
    active      boolean           NOT NULL DEFAULT true,       -- offered in pickers
    CHECK (base_code IS NULL OR base_code <> code)
);
CREATE INDEX language_alt_codes_idx ON language USING gin (alt_codes);

INSERT INTO language (code, name, native_name, modality, alt_codes) VALUES
    -- special
    ('zxx', 'No linguistic content', NULL, 'none', '{}'),
    ('und', 'Undetermined', NULL, 'special', '{}'),
    ('mul', 'Multiple languages', NULL, 'special', '{}'),
    -- spoken
    ('no', 'Norwegian', 'norsk', 'spoken', '{nor}'),
    ('nb', 'Norwegian Bokmål', 'norsk bokmål', 'spoken', '{nob}'),
    ('nn', 'Norwegian Nynorsk', 'norsk nynorsk', 'spoken', '{nno}'),
    ('en', 'English', 'English', 'spoken', '{eng}'),
    ('de', 'German', 'Deutsch', 'spoken', '{ger,deu}'),
    ('nl', 'Dutch', 'Nederlands', 'spoken', '{dut,nld}'),
    ('fr', 'French', 'français', 'spoken', '{fre,fra}'),
    ('es', 'Spanish', 'español', 'spoken', '{spa}'),
    ('pt', 'Portuguese', 'português', 'spoken', '{por}'),
    ('it', 'Italian', 'italiano', 'spoken', '{ita}'),
    ('pl', 'Polish', 'polski', 'spoken', '{pol}'),
    ('ro', 'Romanian', 'română', 'spoken', '{rum,ron}'),
    ('hu', 'Hungarian', 'magyar', 'spoken', '{hun}'),
    ('ru', 'Russian', 'русский', 'spoken', '{rus}'),
    ('uk', 'Ukrainian', 'українська', 'spoken', '{ukr}'),
    ('bg', 'Bulgarian', 'български', 'spoken', '{bul}'),
    ('hr', 'Croatian', 'hrvatski', 'spoken', '{hrv}'),
    ('sl', 'Slovenian', 'slovenščina', 'spoken', '{slv}'),
    ('fi', 'Finnish', 'suomi', 'spoken', '{fin}'),
    ('sv', 'Swedish', 'svenska', 'spoken', '{swe}'),
    ('da', 'Danish', 'dansk', 'spoken', '{dan}'),
    ('tr', 'Turkish', 'Türkçe', 'spoken', '{tur}'),
    ('zh', 'Chinese', '中文', 'spoken', '{chi,zho}'),
    -- signed (ISO 639-3)
    ('nsl', 'Norwegian Sign Language', 'norsk tegnspråk', 'signed', '{}'),
    ('ase', 'American Sign Language', NULL, 'signed', '{}'),
    ('bfi', 'British Sign Language', NULL, 'signed', '{}'),
    ('gsg', 'German Sign Language', 'Deutsche Gebärdensprache', 'signed', '{}'),
    ('dse', 'Dutch Sign Language', 'Nederlandse Gebarentaal', 'signed', '{}'),
    ('fsl', 'French Sign Language', 'langue des signes française', 'signed', '{}');

UPDATE language SET base_code = 'no' WHERE code IN ('nb', 'nn');

-- A rendition where an interpreter signs the content (overlay or separate picture).
ALTER TYPE rendition_kind ADD VALUE 'sign' AFTER 'dub';

ALTER TABLE asset
    ADD COLUMN original_language text REFERENCES language (code) ON DELETE RESTRICT;
CREATE INDEX asset_original_language_idx ON asset (original_language);

ALTER TABLE rendition
    ADD CONSTRAINT rendition_language_fkey FOREIGN KEY (language) REFERENCES language (code) ON DELETE RESTRICT;
CREATE INDEX rendition_language_idx ON rendition (language);
COMMENT ON COLUMN rendition.language IS 'The language this rendition adds or changes: dub audio, subtitle or sign language. zxx for no-language versions.';

ALTER TABLE subtitle_track
    ADD CONSTRAINT subtitle_track_language_fkey FOREIGN KEY (language) REFERENCES language (code) ON DELETE RESTRICT;
CREATE INDEX subtitle_track_language_idx ON subtitle_track (language);

-- +goose Down

DROP INDEX subtitle_track_language_idx;
ALTER TABLE subtitle_track DROP CONSTRAINT subtitle_track_language_fkey;
DROP INDEX rendition_language_idx;
COMMENT ON COLUMN rendition.language IS NULL;
ALTER TABLE rendition DROP CONSTRAINT rendition_language_fkey;
ALTER TABLE asset DROP COLUMN original_language;

-- Postgres cannot drop an enum value, so rebuild rendition_kind without 'sign'.
DELETE FROM rendition WHERE kind = 'sign';
ALTER TYPE rendition_kind RENAME TO rendition_kind_old;
CREATE TYPE rendition_kind AS ENUM ('master', 'encode', 'dub', 'subtitle', 'audio', 'other');
ALTER TABLE rendition ALTER COLUMN kind TYPE rendition_kind USING kind::text::rendition_kind;
DROP TYPE rendition_kind_old;

DROP TABLE language;
DROP TYPE language_modality;
