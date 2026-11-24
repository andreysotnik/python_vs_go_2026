-- +goose Up
-- All list indexes are partial (deleted_at IS NULL): soft-deleted rows never appear in lists,
-- so they do not bloat hot indexes. Every sort index ends with id (keyset tie-breaker).

-- books: sorting
CREATE INDEX books_title_id_idx   ON books (title, id)            WHERE deleted_at IS NULL;
CREATE INDEX books_year_id_idx    ON books (publication_year, id) WHERE deleted_at IS NULL;
CREATE INDEX books_created_id_idx ON books (created_at, id)       WHERE deleted_at IS NULL;
-- books: exact filters
CREATE INDEX books_lang_year_idx  ON books (language_code, publication_year, id) WHERE deleted_at IS NULL;
CREATE INDEX books_publisher_idx  ON books (publisher)            WHERE deleted_at IS NULL;
CREATE INDEX books_created_by_idx ON books (created_by, created_at, id) WHERE deleted_at IS NULL;
CREATE INDEX books_genres_gin     ON books USING gin (genres)     WHERE deleted_at IS NULL;
-- books: search
CREATE INDEX books_search_gin     ON books USING gin (search_vector) WHERE deleted_at IS NULL;
CREATE INDEX books_title_trgm     ON books USING gin (norm_text(title || ' ' || coalesce(original_title, '')) gin_trgm_ops)
    WHERE deleted_at IS NULL;

-- authors
CREATE INDEX authors_name_id_idx    ON authors (full_name, id)  WHERE deleted_at IS NULL;
CREATE INDEX authors_birth_id_idx   ON authors (birth_date, id) WHERE deleted_at IS NULL;
CREATE INDEX authors_created_id_idx ON authors (created_at, id) WHERE deleted_at IS NULL;
CREATE INDEX authors_country_idx    ON authors (country_code, full_name, id) WHERE deleted_at IS NULL;
CREATE INDEX authors_search_gin     ON authors USING gin (search_vector) WHERE deleted_at IS NULL;
CREATE INDEX authors_name_trgm      ON authors USING gin (norm_text(full_name || ' ' || coalesce(original_name, '')) gin_trgm_ops)
    WHERE deleted_at IS NULL;

-- audit history lookup ("who changed entity X and when")
CREATE INDEX audit_entity_idx ON audit_log (entity_type, entity_id, changed_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS audit_entity_idx;
DROP INDEX IF EXISTS authors_name_trgm;
DROP INDEX IF EXISTS authors_search_gin;
DROP INDEX IF EXISTS authors_country_idx;
DROP INDEX IF EXISTS authors_created_id_idx;
DROP INDEX IF EXISTS authors_birth_id_idx;
DROP INDEX IF EXISTS authors_name_id_idx;
DROP INDEX IF EXISTS books_title_trgm;
DROP INDEX IF EXISTS books_search_gin;
DROP INDEX IF EXISTS books_genres_gin;
DROP INDEX IF EXISTS books_created_by_idx;
DROP INDEX IF EXISTS books_publisher_idx;
DROP INDEX IF EXISTS books_lang_year_idx;
DROP INDEX IF EXISTS books_created_id_idx;
DROP INDEX IF EXISTS books_year_id_idx;
DROP INDEX IF EXISTS books_title_id_idx;
