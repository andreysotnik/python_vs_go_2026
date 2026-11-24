-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS btree_gin;
CREATE EXTENSION IF NOT EXISTS unaccent;

-- unaccent() is STABLE (depends on search_path); wrap it with a fixed dictionary so it can be
-- used in generated columns and expression indexes.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION f_unaccent(text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
AS $$ SELECT public.unaccent('public.unaccent'::regdictionary, $1) $$;
-- +goose StatementEnd

-- Normalized form used by trigram (fuzzy / substring) search.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION norm_text(text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
AS $$ SELECT lower(public.f_unaccent($1)) $$;
-- +goose StatementEnd

-- Multilingual full-text configuration: 'simple' parser/dictionary (no stemming, works for any
-- language incl. Cyrillic, Arabic) + unaccent.
CREATE TEXT SEARCH CONFIGURATION simple_unaccent (COPY = simple);
ALTER TEXT SEARCH CONFIGURATION simple_unaccent
    ALTER MAPPING FOR hword, hword_part, word WITH unaccent, simple;

-- +goose Down
DROP TEXT SEARCH CONFIGURATION IF EXISTS simple_unaccent;
DROP FUNCTION IF EXISTS norm_text(text);
DROP FUNCTION IF EXISTS f_unaccent(text);
