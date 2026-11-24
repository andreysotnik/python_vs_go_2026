-- +goose Up

-- users: small, not partitioned. id 1 = system user.
CREATE TABLE users (
    id         bigint PRIMARY KEY,
    username   text        NOT NULL UNIQUE CHECK (username ~ '^[A-Za-z0-9_.-]{3,64}$'),
    email      text        NOT NULL UNIQUE CHECK (length(email) <= 254 AND email ~ '^[^@[:space:]]+@[^@[:space:]]+$'),
    created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO users (id, username, email) VALUES (1, 'system', 'system@library.local');

-- authors: hash-partitioned by id.
CREATE TABLE authors (
    id            bigint      NOT NULL,
    full_name     text        NOT NULL CHECK (length(full_name) BETWEEN 1 AND 512),
    original_name text        CHECK (length(original_name) <= 512),
    birth_date    date        CHECK (birth_date BETWEEN DATE '0001-01-01' AND DATE '2100-12-31'),
    death_date    date        CHECK (death_date BETWEEN DATE '0001-01-01' AND DATE '2100-12-31'),
    country_code  text        CHECK (country_code ~ '^[A-Z]{2}$'),
    biography     text        CHECK (length(biography) <= 20000),
    external_ids  jsonb       NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(external_ids) = 'object'),
    search_vector tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple_unaccent'::regconfig, full_name), 'A') ||
        setweight(to_tsvector('simple_unaccent'::regconfig, coalesce(original_name, '')), 'A')
    ) STORED,
    version       integer     NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at    timestamptz NOT NULL DEFAULT now(),
    created_by    bigint      NOT NULL REFERENCES users (id),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    updated_by    bigint      NOT NULL REFERENCES users (id),
    deleted_at    timestamptz,
    CONSTRAINT authors_dates_chk CHECK (death_date IS NULL OR birth_date IS NULL OR death_date >= birth_date),
    PRIMARY KEY (id)
) PARTITION BY HASH (id);

-- books: hash-partitioned by id.
CREATE TABLE books (
    id               bigint      NOT NULL,
    title            text        NOT NULL CHECK (length(title) BETWEEN 1 AND 1000),
    original_title   text        CHECK (length(original_title) <= 1000),
    subtitle         text        CHECK (length(subtitle) <= 1000),
    description      text        CHECK (length(description) <= 20000),
    isbn_10          text        CHECK (isbn_10 ~ '^[0-9]{9}[0-9X]$'),
    isbn_13          text        CHECK (isbn_13 ~ '^97[89][0-9]{10}$'),
    language_code    text        NOT NULL CHECK (language_code ~ '^[a-z]{2,3}$'),
    publication_year smallint    CHECK (publication_year BETWEEN -3000 AND 2100),
    publisher        text        CHECK (length(publisher) BETWEEN 1 AND 512),
    page_count       integer     CHECK (page_count BETWEEN 1 AND 100000),
    genres           text[]      NOT NULL DEFAULT '{}' CHECK (cardinality(genres) <= 20),
    edition          text        CHECK (length(edition) <= 128),
    external_ids     jsonb       NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(external_ids) = 'object'),
    search_vector    tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple_unaccent'::regconfig, title), 'A') ||
        setweight(to_tsvector('simple_unaccent'::regconfig, coalesce(original_title, '')), 'A') ||
        setweight(to_tsvector('simple_unaccent'::regconfig, coalesce(subtitle, '')), 'B')
    ) STORED,
    version          integer     NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at       timestamptz NOT NULL DEFAULT now(),
    created_by       bigint      NOT NULL REFERENCES users (id),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    updated_by       bigint      NOT NULL REFERENCES users (id),
    deleted_at       timestamptz,
    PRIMARY KEY (id)
) PARTITION BY HASH (id);

-- book -> authors direction, co-located with books (same hash key & modulus).
CREATE TABLE book_authors (
    book_id   bigint   NOT NULL REFERENCES books (id),
    author_id bigint   NOT NULL REFERENCES authors (id),
    role      text     NOT NULL CHECK (role IN ('author', 'co-author', 'translator', 'editor', 'illustrator')),
    ordinal   smallint NOT NULL DEFAULT 1 CHECK (ordinal BETWEEN 1 AND 1000),
    PRIMARY KEY (book_id, author_id, role)
) PARTITION BY HASH (book_id);

-- author -> books direction: denormalized mirror partitioned by author_id, written in the same
-- transaction as book_authors. Avoids scanning all book_authors partitions for "books of author".
CREATE TABLE author_books (
    author_id bigint   NOT NULL REFERENCES authors (id),
    book_id   bigint   NOT NULL REFERENCES books (id),
    role      text     NOT NULL CHECK (role IN ('author', 'co-author', 'translator', 'editor', 'illustrator')),
    ordinal   smallint NOT NULL DEFAULT 1 CHECK (ordinal BETWEEN 1 AND 1000),
    PRIMARY KEY (author_id, book_id, role)
) PARTITION BY HASH (author_id);

-- Global ISBN-13 uniqueness: a UNIQUE index on books(isbn_13) is impossible because the partition
-- key (id) must be part of every unique index. This "global index table" is partitioned by the
-- ISBN itself, so its PK enforces global uniqueness and serves ISBN lookups.
CREATE TABLE book_isbns (
    isbn_13 text   NOT NULL CHECK (isbn_13 ~ '^97[89][0-9]{10}$'),
    book_id bigint NOT NULL REFERENCES books (id),
    PRIMARY KEY (isbn_13)
) PARTITION BY HASH (isbn_13);

-- +goose StatementBegin
DO $$
DECLARE i int;
BEGIN
    FOR i IN 0..63 LOOP
        EXECUTE format('CREATE TABLE books_p%s PARTITION OF books FOR VALUES WITH (MODULUS 64, REMAINDER %s)', lpad(i::text, 2, '0'), i);
        EXECUTE format('CREATE TABLE book_authors_p%s PARTITION OF book_authors FOR VALUES WITH (MODULUS 64, REMAINDER %s)', lpad(i::text, 2, '0'), i);
        EXECUTE format('CREATE TABLE author_books_p%s PARTITION OF author_books FOR VALUES WITH (MODULUS 64, REMAINDER %s)', lpad(i::text, 2, '0'), i);
    END LOOP;
    FOR i IN 0..31 LOOP
        EXECUTE format('CREATE TABLE authors_p%s PARTITION OF authors FOR VALUES WITH (MODULUS 32, REMAINDER %s)', lpad(i::text, 2, '0'), i);
        EXECUTE format('CREATE TABLE book_isbns_p%s PARTITION OF book_isbns FOR VALUES WITH (MODULUS 32, REMAINDER %s)', lpad(i::text, 2, '0'), i);
    END LOOP;
END $$;
-- +goose StatementEnd

-- audit_log: range-partitioned by month on changed_at.
CREATE TABLE audit_log (
    id          bigint      NOT NULL,
    entity_type text        NOT NULL CHECK (entity_type IN ('book', 'author', 'user')),
    entity_id   bigint      NOT NULL,
    action      text        NOT NULL CHECK (action IN ('create', 'update', 'delete')),
    changed_by  bigint      NOT NULL,
    changed_at  timestamptz NOT NULL DEFAULT now(),
    diff        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (changed_at, id)
) PARTITION BY RANGE (changed_at);

CREATE TABLE audit_log_default PARTITION OF audit_log DEFAULT;

-- Creates monthly partitions [from_month, from_month + months). Idempotent; call from cron/migrate.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ensure_audit_partitions(from_month date, months int) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    m date := date_trunc('month', from_month)::date;
    i int;
    part text;
BEGIN
    FOR i IN 0..months - 1 LOOP
        part := format('audit_log_%s', to_char(m, 'YYYY_MM'));
        IF to_regclass(part) IS NULL THEN
            EXECUTE format('CREATE TABLE %I PARTITION OF audit_log FOR VALUES FROM (%L) TO (%L)',
                           part, m, (m + interval '1 month')::date);
        END IF;
        m := (m + interval '1 month')::date;
    END LOOP;
END $$;
-- +goose StatementEnd

SELECT ensure_audit_partitions(date '2023-01-01', 72);

-- +goose Down
DROP FUNCTION IF EXISTS ensure_audit_partitions(date, int);
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS book_isbns;
DROP TABLE IF EXISTS author_books;
DROP TABLE IF EXISTS book_authors;
DROP TABLE IF EXISTS books;
DROP TABLE IF EXISTS authors;
DROP TABLE IF EXISTS users;
