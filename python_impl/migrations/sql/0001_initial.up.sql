-- World Library: initial schema.
-- Design notes live in docs/ARCHITECTURE.md.

CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS btree_gin;
CREATE EXTENSION IF NOT EXISTS unaccent;

-- unaccent() is STABLE (depends on search_path); generated columns and index
-- expressions need an IMMUTABLE wrapper with a fully-qualified dictionary.
CREATE OR REPLACE FUNCTION f_unaccent(text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
    RETURN public.unaccent('public.unaccent'::regdictionary, $1);

-- Snowflake-like 63-bit ids: 41 bits ms since 2020-01-01 | 10 bits node | 12 bits sequence.
-- Monotonic (B-tree friendly), sortable by creation time, unique across tables.
-- Node 1 = database default; seed uses nodes 2..4; every future shard gets its own node id.
CREATE SEQUENCE id_seq CACHE 100;

CREATE OR REPLACE FUNCTION next_id() RETURNS bigint
    LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE AS
$$
DECLARE
    epoch_ms CONSTANT bigint := 1577836800000;
    node     CONSTANT bigint := 1;
    now_ms   bigint;
    seq      bigint;
BEGIN
    seq := nextval('id_seq') % 4096;
    now_ms := floor(extract(epoch FROM clock_timestamp()) * 1000)::bigint;
    RETURN ((now_ms - epoch_ms) << 22) | (node << 12) | seq;
END
$$;

-- ---------------------------------------------------------------- users
CREATE TABLE users (
    id         bigint      PRIMARY KEY DEFAULT next_id(),
    username   text        NOT NULL,
    email      text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_username_chk CHECK (username ~ '^[A-Za-z0-9_.-]{3,64}$'),
    CONSTRAINT users_email_chk CHECK (length(email) <= 254 AND email ~ '^[^@[:space:]]+@[^@[:space:]]+$')
);
CREATE UNIQUE INDEX users_username_uq ON users (lower(username));
CREATE UNIQUE INDEX users_email_uq ON users (lower(email));

INSERT INTO users (id, username, email, created_at)
VALUES (1, 'system', 'system@library.local', '2020-01-01T00:00:00Z');

-- ---------------------------------------------------------------- authors
CREATE TABLE authors (
    id            bigint      NOT NULL DEFAULT next_id(),
    full_name     text        NOT NULL,
    original_name text,
    birth_date    date,
    death_date    date,
    country_code  text,
    biography     text,
    external_ids  jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now(),
    created_by    bigint      NOT NULL REFERENCES users (id),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    updated_by    bigint      NOT NULL REFERENCES users (id),
    deleted_at    timestamptz,
    version       integer     NOT NULL DEFAULT 1,
    search_vector tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple'::regconfig, f_unaccent(full_name)), 'A')
        || setweight(to_tsvector('simple'::regconfig, f_unaccent(coalesce(original_name, ''))), 'A')
    ) STORED,
    CONSTRAINT authors_pk PRIMARY KEY (id),
    CONSTRAINT authors_full_name_chk CHECK (length(full_name) BETWEEN 1 AND 512),
    CONSTRAINT authors_original_name_chk CHECK (original_name IS NULL OR length(original_name) BETWEEN 1 AND 512),
    CONSTRAINT authors_country_chk CHECK (country_code IS NULL OR country_code ~ '^[A-Z]{2}$'),
    CONSTRAINT authors_birth_chk CHECK (birth_date IS NULL OR birth_date BETWEEN DATE '0001-01-01' - INTERVAL '3000 years' AND DATE '2100-12-31'),
    CONSTRAINT authors_death_chk CHECK (death_date IS NULL OR death_date <= DATE '2100-12-31'),
    CONSTRAINT authors_lifespan_chk CHECK (death_date IS NULL OR birth_date IS NULL OR death_date >= birth_date),
    CONSTRAINT authors_external_ids_chk CHECK (jsonb_typeof(external_ids) = 'object'),
    CONSTRAINT authors_version_chk CHECK (version > 0)
) PARTITION BY HASH (id);

DO $$
BEGIN
    FOR i IN 0..31 LOOP
        EXECUTE format('CREATE TABLE authors_p%s PARTITION OF authors FOR VALUES WITH (MODULUS 32, REMAINDER %s)', lpad(i::text, 2, '0'), i);
    END LOOP;
END $$;

-- Sort indexes (always "<sort key>, id" for keyset tie-break); partial: only live rows are listed.
CREATE INDEX authors_full_name_idx  ON authors (full_name, id) WHERE deleted_at IS NULL;
CREATE INDEX authors_birth_idx      ON authors ((coalesce(birth_date, 'infinity'::date)), id) WHERE deleted_at IS NULL;
CREATE INDEX authors_created_idx    ON authors (created_at, id) WHERE deleted_at IS NULL;
CREATE INDEX authors_country_idx    ON authors (country_code) WHERE deleted_at IS NULL;
CREATE INDEX authors_search_idx     ON authors USING gin (search_vector) WHERE deleted_at IS NULL;
CREATE INDEX authors_name_trgm_idx  ON authors USING gin ((f_unaccent(full_name || ' ' || coalesce(original_name, ''))) gin_trgm_ops) WHERE deleted_at IS NULL;

-- ---------------------------------------------------------------- books
CREATE TABLE books (
    id               bigint      NOT NULL DEFAULT next_id(),
    title            text        NOT NULL,
    original_title   text,
    subtitle         text,
    description      text,
    isbn_10          text,
    isbn_13          text,
    language_code    text,
    publication_year smallint,
    publisher        text,
    page_count       integer,
    genres           text[]      NOT NULL DEFAULT '{}'::text[],
    edition          text,
    external_ids     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at       timestamptz NOT NULL DEFAULT now(),
    created_by       bigint      NOT NULL REFERENCES users (id),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    updated_by       bigint      NOT NULL REFERENCES users (id),
    deleted_at       timestamptz,
    version          integer     NOT NULL DEFAULT 1,
    search_vector    tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple'::regconfig, f_unaccent(title)), 'A')
        || setweight(to_tsvector('simple'::regconfig, f_unaccent(coalesce(original_title, ''))), 'A')
        || setweight(to_tsvector('simple'::regconfig, f_unaccent(coalesce(subtitle, ''))), 'B')
        || setweight(to_tsvector('simple'::regconfig, f_unaccent(coalesce(description, ''))), 'D')
    ) STORED,
    CONSTRAINT books_pk PRIMARY KEY (id),
    CONSTRAINT books_title_chk CHECK (length(title) BETWEEN 1 AND 1024),
    CONSTRAINT books_isbn10_chk CHECK (isbn_10 IS NULL OR isbn_10 ~ '^[0-9]{9}[0-9X]$'),
    CONSTRAINT books_isbn13_chk CHECK (isbn_13 IS NULL OR isbn_13 ~ '^97[89][0-9]{10}$'),
    CONSTRAINT books_language_chk CHECK (language_code IS NULL OR language_code ~ '^[a-z]{2,3}$'),
    CONSTRAINT books_year_chk CHECK (publication_year IS NULL OR publication_year BETWEEN -3000 AND 2100),
    CONSTRAINT books_page_count_chk CHECK (page_count IS NULL OR page_count BETWEEN 1 AND 100000),
    CONSTRAINT books_genres_chk CHECK (cardinality(genres) <= 16),
    CONSTRAINT books_external_ids_chk CHECK (jsonb_typeof(external_ids) = 'object'),
    CONSTRAINT books_version_chk CHECK (version > 0)
) PARTITION BY HASH (id);

DO $$
BEGIN
    FOR i IN 0..63 LOOP
        EXECUTE format('CREATE TABLE books_p%s PARTITION OF books FOR VALUES WITH (MODULUS 64, REMAINDER %s)', lpad(i::text, 2, '0'), i);
    END LOOP;
END $$;

CREATE INDEX books_title_idx      ON books (title, id) WHERE deleted_at IS NULL;
CREATE INDEX books_year_idx       ON books ((coalesce(publication_year, 32767::smallint)), id) WHERE deleted_at IS NULL;
CREATE INDEX books_created_idx    ON books (created_at, id) WHERE deleted_at IS NULL;
CREATE INDEX books_language_idx   ON books (language_code) WHERE deleted_at IS NULL;
CREATE INDEX books_publisher_idx  ON books (publisher) WHERE deleted_at IS NULL;
CREATE INDEX books_created_by_idx ON books (created_by) WHERE deleted_at IS NULL;
CREATE INDEX books_genres_idx     ON books USING gin (genres) WHERE deleted_at IS NULL;
CREATE INDEX books_search_idx     ON books USING gin (search_vector) WHERE deleted_at IS NULL;
CREATE INDEX books_title_trgm_idx ON books USING gin ((f_unaccent(title)) gin_trgm_ops) WHERE deleted_at IS NULL;

-- Global ISBN-13 uniqueness: a UNIQUE index on books must contain the partition key (id),
-- so uniqueness is enforced by a separate lookup table partitioned by the ISBN itself.
-- It also serves "find by ISBN" with a single-partition probe instead of 64.
CREATE TABLE book_isbns (
    isbn_13 text   NOT NULL,
    book_id bigint NOT NULL,
    CONSTRAINT book_isbns_pk PRIMARY KEY (isbn_13)
) PARTITION BY HASH (isbn_13);

DO $$
BEGIN
    FOR i IN 0..15 LOOP
        EXECUTE format('CREATE TABLE book_isbns_p%s PARTITION OF book_isbns FOR VALUES WITH (MODULUS 16, REMAINDER %s)', lpad(i::text, 2, '0'), i);
    END LOOP;
END $$;

-- ---------------------------------------------------------------- book <-> author
-- The relation is stored twice, each copy partitioned (and later sharded) by its lookup key:
--   book_authors  by book_id   -> "authors of a book"   (co-located with books)
--   author_books  by author_id -> "books of an author"  (co-located with authors)
-- Both copies are written in the same transaction by the service layer.
CREATE TABLE book_authors (
    book_id   bigint   NOT NULL,
    author_id bigint   NOT NULL,
    role      text     NOT NULL,
    ordinal   smallint NOT NULL DEFAULT 1,
    CONSTRAINT book_authors_pk PRIMARY KEY (book_id, author_id, role),
    CONSTRAINT book_authors_role_chk CHECK (role IN ('author', 'co-author', 'translator', 'editor', 'illustrator')),
    CONSTRAINT book_authors_ordinal_chk CHECK (ordinal BETWEEN 1 AND 1000),
    CONSTRAINT book_authors_book_fk FOREIGN KEY (book_id) REFERENCES books (id),
    CONSTRAINT book_authors_author_fk FOREIGN KEY (author_id) REFERENCES authors (id)
) PARTITION BY HASH (book_id);

CREATE TABLE author_books (
    author_id bigint   NOT NULL,
    book_id   bigint   NOT NULL,
    role      text     NOT NULL,
    ordinal   smallint NOT NULL DEFAULT 1,
    CONSTRAINT author_books_pk PRIMARY KEY (author_id, book_id, role),
    CONSTRAINT author_books_role_chk CHECK (role IN ('author', 'co-author', 'translator', 'editor', 'illustrator')),
    CONSTRAINT author_books_ordinal_chk CHECK (ordinal BETWEEN 1 AND 1000),
    CONSTRAINT author_books_book_fk FOREIGN KEY (book_id) REFERENCES books (id),
    CONSTRAINT author_books_author_fk FOREIGN KEY (author_id) REFERENCES authors (id)
) PARTITION BY HASH (author_id);

DO $$
BEGIN
    FOR i IN 0..63 LOOP
        EXECUTE format('CREATE TABLE book_authors_p%s PARTITION OF book_authors FOR VALUES WITH (MODULUS 64, REMAINDER %s)', lpad(i::text, 2, '0'), i);
        EXECUTE format('CREATE TABLE author_books_p%s PARTITION OF author_books FOR VALUES WITH (MODULUS 64, REMAINDER %s)', lpad(i::text, 2, '0'), i);
    END LOOP;
END $$;

-- ---------------------------------------------------------------- audit log
CREATE TABLE audit_log (
    id          bigint      NOT NULL DEFAULT next_id(),
    entity_type text        NOT NULL,
    entity_id   bigint      NOT NULL,
    action      text        NOT NULL,
    changed_by  bigint      NOT NULL,
    changed_at  timestamptz NOT NULL DEFAULT now(),
    diff        jsonb,
    CONSTRAINT audit_log_pk PRIMARY KEY (changed_at, id),
    CONSTRAINT audit_log_entity_chk CHECK (entity_type IN ('book', 'author')),
    CONSTRAINT audit_log_action_chk CHECK (action IN ('create', 'update', 'delete'))
) PARTITION BY RANGE (changed_at);

CREATE INDEX audit_log_entity_idx ON audit_log (entity_type, entity_id, changed_at, id);

-- Creates monthly partitions [from_month, from_month + months). Idempotent; call from cron/pg_partman.
CREATE OR REPLACE FUNCTION ensure_audit_partitions(from_month date, months integer) RETURNS void
    LANGUAGE plpgsql AS
$$
DECLARE
    m date := date_trunc('month', from_month)::date;
BEGIN
    FOR i IN 0..months - 1 LOOP
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF audit_log FOR VALUES FROM (%L) TO (%L)',
            'audit_log_' || to_char(m, 'YYYY_MM'), m, (m + INTERVAL '1 month')::date);
        m := (m + INTERVAL '1 month')::date;
    END LOOP;
END
$$;

SELECT ensure_audit_partitions(DATE '2020-01-01', 12 * 8);  -- 2020-01 .. 2027-12
CREATE TABLE audit_log_default PARTITION OF audit_log DEFAULT;
