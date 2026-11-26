# World Library — architecture

Target: 10+ billion books, hundreds of millions to billions of authors, tens of billions of
book↔author links, read-heavy traffic with steady writes, full audit trail.
Stack: Rust 2024 · axum 0.8 · tokio · sqlx 0.8 (PostgreSQL driver + embedded migrations) · serde ·
PostgreSQL 17 (pg_trgm, btree_gin, unaccent, pg_stat_statements).

## 1. Layers

```
HTTP (src/http.rs)          axum router, extractors with RFC 7807 rejections, auth middleware
  │                         (X-User-Id -> Identity), access log + metrics middleware, ETag/If-Match
  ▼
Service (src/service.rs)    validation, transactions, optimistic locking, audit diff, enrichment
  │                         (authors / user names for a page in 2 batched queries)
  ▼
Repository (src/repository.rs)  SQL only; every function takes an explicit connection/tx
  ▲
Pure helpers: query.rs (filter/sort/keyset SQL builders), validation.rs, cursor.rs, isbn.rs, ids.rs
Tools in the same binary: migrate (sqlx migrate), seed (COPY loader), explain (plan checker), healthcheck
```

No global state: the pool, the id generator and the Prometheus registry live in `AppState`.
Configuration is environment-only (`src/config.rs`).

## 2. Schema

```
users(id PK, username UQ(lower), email UQ(lower), created_at)                    -- small, not partitioned

authors  HASH(id) x32      id, full_name, original_name, birth_date, death_date, country_code,
                           biography, external_ids jsonb, search_vector (generated),
                           created_at/by, updated_at/by, deleted_at, version
books    HASH(id) x64      id, title, original_title, subtitle, description, isbn_10, isbn_13,
                           language_code, publication_year, publisher, page_count, genres text[],
                           edition, external_ids jsonb, search_vector (generated),
                           created_at/by, updated_at/by, deleted_at, version
isbn_registry HASH(isbn_13) x16   isbn_13 PK, book_id          -- global ISBN uniqueness
book_authors  HASH(book_id) x64   PK(book_id, author_id, role), ordinal   -- "authors of a book"
author_books  HASH(author_id) x64 PK(author_id, book_id, role), ordinal   -- "books of an author"
audit_log RANGE(changed_at) monthly   id, entity_type, entity_id, action, changed_by, changed_at, diff jsonb
```

* **Ids** — 64-bit snowflake-like (`41 bits ms since 2020-01-01 | 10 bits node | 12 bits seq`),
  generated in the app (`NODE_ID` per replica). Monotonic per node → append-mostly B-trees,
  no UUIDv4. Sortable by creation time, unique across shards without coordination, carry no
  sequence/locking hot spot. Serialized as JSON **strings** (values > 2^53).
* **CHECK constraints**: language `^[a-z]{2,3}$` (ISO 639-1/3), country `^[A-Z]{2}$`, years
  −3000..2100, dates, `death_date >= birth_date`, ISBN formats, length limits, roles enum,
  `jsonb_typeof(external_ids) = 'object'`. The app validates the same rules first (good messages),
  the DB is the last line of defence.
* **Soft delete**: `deleted_at`; every serving index is partial `WHERE deleted_at IS NULL`, so
  deleted rows cost nothing for reads and the history remains.
* **Optimistic locking**: `version` (books and authors). `UPDATE … WHERE id=$1 AND version=$2`,
  plus `SELECT … FOR UPDATE` to compute the diff; stale → 409, missing → 428.
* **Audit**: every create/update/delete writes `audit_log` in the same transaction:
  `create` = snapshot of non-null fields, `update` = `{field: {old, new}}` (author links included),
  `delete`. `created_by/updated_by` on the row answer "who/when" in O(1); the log answers "what".

### Global ISBN-13 uniqueness on a partitioned table
A unique index on a partitioned table must contain the partition key, so `UNIQUE(isbn_13)` on
`books` (partitioned by id) is impossible. Solution: `isbn_registry(isbn_13 PK)` partitioned by
the ISBN itself, written in the same transaction as the book (insert → unique violation → 409).
It doubles as the lookup index for `?isbn=`: `b.id = (SELECT book_id FROM isbn_registry WHERE
isbn_13 = $1)` → one registry probe + one pruned PK probe. ISBN-10 input is converted to ISBN-13.
Soft delete releases the ISBN. With sharding the registry is its own distributed table keyed by ISBN.

## 3. Partitioning

* `books`, `book_authors` (HASH(book_id)) and `author_books` (HASH(author_id)) — 64 partitions,
  `authors` — 32, `isbn_registry` — 16. Hash by id spreads writes evenly (ids are time-ordered, so
  range partitioning by id would create a single hot partition) and keeps every partition a
  manageable size for VACUUM / index builds (10B / 64 ≈ 156M rows; for production at 10B we would
  go to 256 partitions — the count is a migration constant).
* Links are stored **twice**, each copy partitioned by its "owner": `book_authors` is co-located
  with `books` (same key, same modulus) and `author_books` with `authors`. Both directions are
  single-partition (later: single-shard) lookups. They are written in one transaction; a
  consistency test checks that both sides match.
* `audit_log` is RANGE-partitioned by month: old months can be detached/archived cheaply;
  `ensure_audit_partitions()` pre-creates months (migration + app start-up).
* **Path to sharding** (Citus or manual): `books`, `book_authors`, `isbn_registry`… become
  distributed tables: `books` + `book_authors` distributed by book id (co-located), `authors` +
  `author_books` by author id (co-located), `isbn_registry` by ISBN, `users` a reference table.
  No query joins across a distribution key except via id lookups, keyset cursors carry only
  (sort value, id), and ids are generated without a central sequence — the API does not change.

## 4. Indexes (and why only these)

| table | index | serves |
|---|---|---|
| books | PK(id) | card, enrichment, ISBN lookup, author→books join |
| books | (title, id) | sort=title, keyset |
| books | (language_code, title, id) | `language=X&sort=title` (found by the plan check: CJK/Arabic titles sort after Latin ones, so "title index + filter" scanned 870k entries) |
| books | (coalesce(publication_year, 32767), id) | sort=publication_year + year range (NULL years sort last; sentinel keeps keyset predicates index-friendly) |
| books | (created_at, id) | default sort, created_from/to |
| books | GIN(search_vector) | `q` full text, search |
| books | GIN trgm(f_unaccent(title‖original_title)) | `title` substring/prefix, fuzzy search |
| books | GIN(language_code, publisher, genres, created_by) — btree_gin | equality filters in any combination, one index instead of four B-trees |
| authors | (full_name,id), (coalesce(birth_date,'infinity'),id), (created_at,id), (country_code, full_name, id), GIN fts, GIN trgm(names) | same pattern |
| book_authors / author_books | PK only | both link directions |
| audit_log | (entity_type, entity_id, changed_at, id) | history keyset |

All partial (`WHERE deleted_at IS NULL`). Deliberately **not** added: separate B-trees on
publisher/genre/created_by/language (the multi-column GIN covers them, any subset; GIN with
`fastupdate` is cheaper to maintain than four B-trees), indexes for every filter×sort combination
(write amplification on 10B rows), ISBN index on books (the registry is the index). Write cost per
book insert: ~9 index entries + 2 link rows + registry + audit.

Sort whitelist: books `title | publication_year | created_at`, authors
`full_name | birth_date | created_at` (± prefix), always `id` as the tie-breaker — exactly the
columns that have a `(sort_expr, id)` index.

## 5. Pagination

Keyset only. Cursor = base64url(JSON `{k: "books:title", d: desc?, v: last sort value, i: last id}`);
it is bound to the endpoint+sort (a cursor from another sort → 400). Predicate:
`(sort_expr, id) > ($v, $id)` (row comparison → an index condition, works through MergeAppend
across partitions), `ORDER BY sort_expr, id LIMIT limit+1` (the extra row tells whether there is a
next page). `limit` ≤ 100. No OFFSET anywhere (the plan checker asserts this).

`total` is only computed on request (`with_total=true`) and never with COUNT(*): without filters
it is `sum(pg_class.reltuples)` over partitions, with filters the planner's row estimate from
`EXPLAIN (FORMAT JSON)`; `total_is_estimate: true`.

## 6. Search

* `search_vector` is a STORED generated column: `to_tsvector('simple', f_unaccent(...))`
  with weights (title/original A, subtitle B; author names A). `'simple'` because the catalog is
  multilingual — no stemming that would be wrong for most languages; `f_unaccent` is an IMMUTABLE
  wrapper around `unaccent` with an explicit dictionary (required in generated columns/indexes).
* Substring/prefix/fuzzy: pg_trgm GIN over the unaccented title/name expression; `title`/`name`
  filters require ≥ 3 characters (shorter patterns cannot use trigrams).
* `/search`: **full-text mode** — `search_vector @@ websearch_to_tsquery(...)`, the best 1000
  matches by `ts_rank` (deterministic: `ORDER BY ts_rank, id`) are re-ranked by
  `ts_rank + similarity(title)`. If full text finds nothing (typo, partial word), **fuzzy mode** —
  `q <% title` (trigram word similarity, GIN-indexed), rank = `word_similarity`. The mode is stored
  in the cursor; keyset over `(rank, type, id)`.
  Measured: computing `similarity` for every match of a frequent word was 80% of the query
  (25k matches: 100 ms → ~25 ms with the 1000-candidate cap).
* Limitations: no stemming/morphology; CJK text is not segmented by the `simple` parser
  (whole runs are tokens — trigram search still finds substrings); relevance work is O(matches).

### When to add OpenSearch/Elasticsearch
When relevance search becomes a large share of DB CPU (it already is the heaviest endpoint in the
load test), or language-specific analysis (morphology, CJK segmentation, synonyms), facets,
typo-tolerance at scale are required. How: keep PostgreSQL the source of truth and stream changes
via **CDC** — either a transactional **outbox** table written in the same transaction as the
audit entry (Debezium outbox router → Kafka → indexer) or **logical replication** (`wal_level =
logical`, publication on books/authors/links, Debezium/pgoutput). The indexer denormalizes a book
document with its authors; `/search` switches to the engine, while list/filter endpoints with
keyset pagination stay in PostgreSQL. Reindexing = snapshot + replay from the slot/outbox offset.

## 7. Operational aspects

* **Timeouts**: every request runs in a transaction that starts with
  `set_config('statement_timeout', …, true)` (transaction-local → PgBouncer transaction-mode safe;
  no session state is used anywhere). Timeout → 503 problem. Plus request timeout (504),
  `idle_in_transaction_session_timeout`, `lock_timeout` in postgresql.conf.
* **PgBouncer**: transaction pooling compatible (no session `SET`, no advisory locks, no LISTEN,
  no temp tables). sqlx uses named prepared statements → PgBouncer ≥ 1.21 with
  `max_prepared_statements > 0`, or set `DB_STATEMENT_CACHE_CAPACITY=0`.
* **Generic plans**: prepared statements switch to a cached generic plan after 5 executions.
  Two queries had a fine custom plan but a generic plan with a Seq Scan (authors-of-books via
  `= ANY($1)`, author-name filter): both were rewritten as LATERAL primary-key lookups
  (`LIMIT 1` as an optimization fence) and the plan checker now validates generic plans too
  (`EXPLAIN (GENERIC_PLAN)`). Forcing custom plans instead cost ~4 ms planning per query
  (64 partitions) and 3x throughput.
* **Bulk load**: `COPY` in parallel chunks (8 connections), secondary indexes and FKs dropped
  before and rebuilt after: each index is created `ON ONLY` the parent, built per partition in
  parallel, then `ATTACH`ed — the same procedure (with `CONCURRENTLY`) works on a live system.
  DDL to restore is stored in `seed_pending_ddl`, so an interrupted run recovers.
* **Observability**: JSON logs (tracing), access log with request id / route / status / latency /
  user, `/metrics` (requests, latency histogram by route, pool gauges), `/healthz`, `/readyz`,
  pg_stat_statements enabled.

## 8. Known limitations / next steps

* `with_total` estimates can be far off for correlated filters (by design: it is an estimate).
* Relevance search is CPU-heavy for very frequent terms (bounded by the candidate cap and
  statement_timeout) → external search engine at scale.
* `author` name filter on books materializes all matching authors' books before sorting
  (index nested loops, but O(books of matching authors)); very broad names are slow.
* Links are mirrored by application code, not a trigger (keeps COPY fast); a periodic
  reconciliation job would be needed in production.
* Authentication is a trusted header (`X-User-Id`) — replace the `authenticate` middleware with
  JWT validation; authorization (roles) is not implemented.
* Partition counts are fixed in the migration; growing beyond needs re-partitioning or sharding.
