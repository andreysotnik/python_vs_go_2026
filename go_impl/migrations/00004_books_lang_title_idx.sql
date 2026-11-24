-- +goose Up
-- EXPLAIN on the seeded DB showed "language=ru sort=title" walking ~10k index entries per
-- partition (titles in a script sort far from Latin ones, so the filter is collation-correlated).
-- Language is the main catalog facet, so it gets a composite with the title sort key.
-- NOTE: on a live 10B-row table build per partition with CREATE INDEX CONCURRENTLY and
-- ALTER INDEX ... ATTACH PARTITION instead (CONCURRENTLY is not supported on the parent).
CREATE INDEX books_lang_title_idx ON books (language_code, title, id) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS books_lang_title_idx;
