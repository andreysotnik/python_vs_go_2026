-- World Library: initial schema.
-- See docs/ARCHITECTURE.md for the reasoning behind partitioning and indexes.

CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS btree_gin;
CREATE EXTENSION IF NOT EXISTS unaccent;

-- unaccent() is STABLE (it depends on search_path / dictionary lookup), so it cannot be
-- used in generated columns or index expressions directly. This wrapper pins the
-- dictionary explicitly and is therefore safe to mark IMMUTABLE.
CREATE OR REPLACE FUNCTION f_unaccent(text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
AS $$ SELECT public.unaccent('public.unaccent'::regdictionary, $1) $$;

-------------------------------------------------------------------------------
-- users (small, not partitioned)
-------------------------------------------------------------------------------
CREATE TABLE users (
    id          bigint      PRIMARY KEY,
    username    text        NOT NULL CHECK (username ~ '^[A-Za-z0-9_.-]{3,64}$'),
    email       text        NOT NULL CHECK (email ~ '^[^@\s]+@[^@\s]+$' AND length(email) <= 254),
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_username_uq ON users (lower(username));
CREATE UNIQUE INDEX users_email_uq ON users (lower(email));

-------------------------------------------------------------------------------
-- authors: HASH(id) x 32
-------------------------------------------------------------------------------
CREATE TABLE authors (
    id             bigint      NOT NULL,
    full_name      text        NOT NULL CHECK (length(full_name) BETWEEN 1 AND 512),
    original_name  text        CHECK (length(original_name) BETWEEN 1 AND 512),
    birth_date     date        CHECK (birth_date BETWEEN '3000-01-01 BC' AND '2100-12-31'),
    death_date     date        CHECK (death_date BETWEEN '3000-01-01 BC' AND '2100-12-31'),
    country_code   text        CHECK (country_code ~ '^[A-Z]{2}$'),
    biography      text        CHECK (length(biography) <= 20000),
    external_ids   jsonb       NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(external_ids) = 'object'),
    search_vector  tsvector    GENERATED ALWAYS AS (
        setweight(to_tsvector('simple'::regconfig, f_unaccent(full_name)), 'A') ||
        setweight(to_tsvector('simple'::regconfig, f_unaccent(coalesce(original_name, ''))), 'A')
    ) STORED,
    created_at     timestamptz NOT NULL DEFAULT now(),
    created_by     bigint      NOT NULL REFERENCES users (id),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    updated_by     bigint      NOT NULL REFERENCES users (id),
    deleted_at     timestamptz,
    version        integer     NOT NULL DEFAULT 1 CHECK (version >= 1),
    CONSTRAINT authors_dates_chk CHECK (death_date IS NULL OR birth_date IS NULL OR death_date >= birth_date),
    PRIMARY KEY (id)
) PARTITION BY HASH (id);

DO $$
BEGIN
    FOR i IN 0..31 LOOP
        EXECUTE format('CREATE TABLE authors_p%s PARTITION OF authors FOR VALUES WITH (MODULUS 32, REMAINDER %s)', lpad(i::text, 2, '0'), i);
    END LOOP;
END $$;

-- Sorting / keyset indexes (live rows only); id is always the tie-breaker.
CREATE INDEX authors_full_name_idx  ON authors (full_name, id) WHERE deleted_at IS NULL;
CREATE INDEX authors_birth_idx      ON authors ((coalesce(birth_date, 'infinity'::date)), id) WHERE deleted_at IS NULL;
CREATE INDEX authors_created_idx    ON authors (created_at, id) WHERE deleted_at IS NULL;
-- Country filter + default name ordering inside a country.
CREATE INDEX authors_country_idx    ON authors (country_code, full_name, id) WHERE deleted_at IS NULL;
-- Full-text search and fuzzy/substring search on names.
CREATE INDEX authors_fts_idx        ON authors USING gin (search_vector) WHERE deleted_at IS NULL;
CREATE INDEX authors_name_trgm_idx  ON authors USING gin (f_unaccent(full_name || ' ' || coalesce(original_name, '')) gin_trgm_ops) WHERE deleted_at IS NULL;

-------------------------------------------------------------------------------
-- books: HASH(id) x 64
-------------------------------------------------------------------------------
CREATE TABLE books (
    id                bigint      NOT NULL,
    title             text        NOT NULL CHECK (length(title) BETWEEN 1 AND 1000),
    original_title    text        CHECK (length(original_title) BETWEEN 1 AND 1000),
    subtitle          text        CHECK (length(subtitle) <= 1000),
    description       text        CHECK (length(description) <= 20000),
    isbn_10           text        CHECK (isbn_10 ~ '^[0-9]{9}[0-9X]$'),
    isbn_13           text        CHECK (isbn_13 ~ '^97[89][0-9]{10}$'),
    language_code     text        NOT NULL CHECK (language_code ~ '^[a-z]{2,3}$'),
    publication_year  smallint    CHECK (publication_year BETWEEN -3000 AND 2100),
    publisher         text        CHECK (length(publisher) BETWEEN 1 AND 512),
    page_count        integer     CHECK (page_count BETWEEN 1 AND 100000),
    genres            text[]      NOT NULL DEFAULT '{}' CHECK (cardinality(genres) <= 20),
    edition           text        CHECK (length(edition) <= 200),
    external_ids      jsonb       NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(external_ids) = 'object'),
    search_vector     tsvector    GENERATED ALWAYS AS (
        setweight(to_tsvector('simple'::regconfig, f_unaccent(title)), 'A') ||
        setweight(to_tsvector('simple'::regconfig, f_unaccent(coalesce(original_title, ''))), 'A') ||
        setweight(to_tsvector('simple'::regconfig, f_unaccent(coalesce(subtitle, ''))), 'B')
    ) STORED,
    created_at        timestamptz NOT NULL DEFAULT now(),
    created_by        bigint      NOT NULL REFERENCES users (id),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    updated_by        bigint      NOT NULL REFERENCES users (id),
    deleted_at        timestamptz,
    version           integer     NOT NULL DEFAULT 1 CHECK (version >= 1),
    PRIMARY KEY (id)
) PARTITION BY HASH (id);

DO $$
BEGIN
    FOR i IN 0..63 LOOP
        EXECUTE format('CREATE TABLE books_p%s PARTITION OF books FOR VALUES WITH (MODULUS 64, REMAINDER %s)', lpad(i::text, 2, '0'), i);
    END LOOP;
END $$;

CREATE INDEX books_title_idx      ON books (title, id) WHERE deleted_at IS NULL;
-- NULL years sort last (ASC) / first (DESC) via a sentinel, which keeps keyset predicates index-friendly.
CREATE INDEX books_year_idx       ON books ((coalesce(publication_year, 32767::smallint)), id) WHERE deleted_at IS NULL;
CREATE INDEX books_created_idx    ON books (created_at, id) WHERE deleted_at IS NULL;
CREATE INDEX books_fts_idx        ON books USING gin (search_vector) WHERE deleted_at IS NULL;
CREATE INDEX books_title_trgm_idx ON books USING gin (f_unaccent(title || ' ' || coalesce(original_title, '')) gin_trgm_ops) WHERE deleted_at IS NULL;
-- One multi-column GIN (btree_gin) for the low-selectivity equality filters instead of
-- four separate B-trees: any subset of these columns can be combined via one index.
CREATE INDEX books_attrs_gin_idx  ON books USING gin (language_code, publisher, genres, created_by) WHERE deleted_at IS NULL;

-------------------------------------------------------------------------------
-- Global ISBN-13 uniqueness. A UNIQUE index on a partitioned table must include
-- the partition key (id), so uniqueness of isbn_13 is enforced by a separate
-- registry table partitioned by the ISBN itself.
-------------------------------------------------------------------------------
CREATE TABLE isbn_registry (
    isbn_13  text   NOT NULL,
    book_id  bigint NOT NULL,
    PRIMARY KEY (isbn_13)
) PARTITION BY HASH (isbn_13);

DO $$
BEGIN
    FOR i IN 0..15 LOOP
        EXECUTE format('CREATE TABLE isbn_registry_p%s PARTITION OF isbn_registry FOR VALUES WITH (MODULUS 16, REMAINDER %s)', lpad(i::text, 2, '0'), i);
    END LOOP;
END $$;

-------------------------------------------------------------------------------
-- book <-> author links, stored twice so that both directions are a
-- single-partition lookup (co-located with the owning entity when sharded).
-------------------------------------------------------------------------------
CREATE TABLE book_authors (
    book_id    bigint   NOT NULL,
    author_id  bigint   NOT NULL,
    role       text     NOT NULL CHECK (role IN ('author', 'co-author', 'translator', 'editor', 'illustrator')),
    ordinal    smallint NOT NULL DEFAULT 1 CHECK (ordinal BETWEEN 1 AND 1000),
    PRIMARY KEY (book_id, author_id, role)
) PARTITION BY HASH (book_id);

CREATE TABLE author_books (
    author_id  bigint   NOT NULL,
    book_id    bigint   NOT NULL,
    role       text     NOT NULL CHECK (role IN ('author', 'co-author', 'translator', 'editor', 'illustrator')),
    ordinal    smallint NOT NULL DEFAULT 1 CHECK (ordinal BETWEEN 1 AND 1000),
    PRIMARY KEY (author_id, book_id, role)
) PARTITION BY HASH (author_id);

DO $$
BEGIN
    FOR i IN 0..63 LOOP
        EXECUTE format('CREATE TABLE book_authors_p%s PARTITION OF book_authors FOR VALUES WITH (MODULUS 64, REMAINDER %s)', lpad(i::text, 2, '0'), i);
        EXECUTE format('CREATE TABLE author_books_p%s PARTITION OF author_books FOR VALUES WITH (MODULUS 64, REMAINDER %s)', lpad(i::text, 2, '0'), i);
    END LOOP;
END $$;

ALTER TABLE book_authors ADD CONSTRAINT book_authors_book_fk   FOREIGN KEY (book_id)   REFERENCES books (id);
ALTER TABLE book_authors ADD CONSTRAINT book_authors_author_fk FOREIGN KEY (author_id) REFERENCES authors (id);

-------------------------------------------------------------------------------
-- audit_log: RANGE(changed_at) by month
-------------------------------------------------------------------------------
CREATE TABLE audit_log (
    id           bigint      NOT NULL,
    entity_type  text        NOT NULL CHECK (entity_type IN ('book', 'author', 'user')),
    entity_id    bigint      NOT NULL,
    action       text        NOT NULL CHECK (action IN ('create', 'update', 'delete')),
    changed_by   bigint      NOT NULL,
    changed_at   timestamptz NOT NULL DEFAULT now(),
    diff         jsonb       NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (id, changed_at)
) PARTITION BY RANGE (changed_at);

CREATE INDEX audit_log_entity_idx ON audit_log (entity_type, entity_id, changed_at, id);

-- Idempotently creates monthly partitions [from_month, from_month + months).
CREATE OR REPLACE FUNCTION ensure_audit_partitions(from_month date, months integer) RETURNS void
    LANGUAGE plpgsql AS $$
DECLARE
    m date := date_trunc('month', from_month)::date;
    i integer;
BEGIN
    FOR i IN 0..months - 1 LOOP
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF audit_log FOR VALUES FROM (%L) TO (%L)',
            'audit_log_y' || to_char(m, 'YYYY') || 'm' || to_char(m, 'MM'),
            m, (m + interval '1 month')::date);
        m := (m + interval '1 month')::date;
    END LOOP;
END $$;

SELECT ensure_audit_partitions('2020-01-01'::date,
    (extract(year FROM age(date_trunc('month', now()) + interval '24 months', '2020-01-01'::date)) * 12
     + extract(month FROM age(date_trunc('month', now()) + interval '24 months', '2020-01-01'::date)))::integer);

-- Bootstrap actor used by README examples and maintenance jobs.
INSERT INTO users (id, username, email) VALUES (1, 'system', 'system@example.org');
