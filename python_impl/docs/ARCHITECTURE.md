# World Library — architecture

Target: 10+ billion books, hundreds of millions to billions of authors, tens of billions of
book↔author links. This repository runs on one PostgreSQL node, but every decision below is taken so
that the same API keeps working once the data is spread over many shards.

## 1. Layers

```
HTTP (FastAPI, pydantic v2)            src/library/api/*        thin: parse -> service -> serialise
  middleware: Observability (JSON access log, Prometheus), Auth (X-User-Id -> Principal)
services (use-cases, transactions)     src/library/services/*   optimistic locking, audit, ISBN registry
repositories (SQL only)                src/library/repositories/* hand-written SQL + tiny QueryBuilder
domain (pure)                          src/library/domain/*     models, cursor, sort whitelist, ISBN, ids
db                                     src/library/db.py        asyncpg pool, Database.tx()
```

There is no global state. `create_app(settings)` builds the pool in the lifespan, `build_container()`
wires the repositories and services explicitly, and handlers get them from `app.state` through a
dependency. Authentication is a pluggable `Authenticator` protocol, so `HeaderAuthenticator`
(trusted `X-User-Id`) can be replaced by a JWT authenticator without touching the handlers.

## 2. Schema

```
users(id PK, username UQ(lower), email UQ(lower), created_at)

authors  PARTITION BY HASH(id) x32
  id bigint (snowflake), full_name, original_name, birth_date, death_date, country_code, biography,
  external_ids jsonb, created_at/by, updated_at/by, deleted_at, version,
  search_vector tsvector GENERATED (full_name A + original_name A, 'simple' + unaccent)

books    PARTITION BY HASH(id) x64
  id, title, original_title, subtitle, description, isbn_10, isbn_13, language_code, publication_year,
  publisher, page_count, genres text[], edition, external_ids jsonb, audit columns, deleted_at, version,
  search_vector GENERATED (title A, original_title A, subtitle B, description D)

book_isbns    PARTITION BY HASH(isbn_13) x16   (isbn_13 PK, book_id)    global ISBN uniqueness
book_authors  PARTITION BY HASH(book_id) x64   PK(book_id, author_id, role)   "authors of a book"
author_books  PARTITION BY HASH(author_id) x64 PK(author_id, book_id, role)   "books of an author"
audit_log     PARTITION BY RANGE(changed_at) monthly (+ DEFAULT), PK(changed_at, id),
              idx(entity_type, entity_id, changed_at, id)
```

CHECK constraints cover language codes (`^[a-z]{2,3}$`), country codes (`^[A-Z]{2}$`), year ranges
(-3000..2100), ISBN formats, page counts, `death_date >= birth_date`, `jsonb_typeof(external_ids) = 'object'`,
role enum and `version > 0`. The API validates the same rules (with ISBN check digits) and returns 422
before any write reaches the database.

### Identifiers
The ids are 63-bit snowflakes: `41 bits ms since 2020-01-01 | 10 bits node | 12 bits sequence`.
`next_id()` in SQL generates them with node 1, the seed uses nodes 2–6, and each future shard gets its
own node id. They are:
* monotonic, so B-tree inserts land on the right-most page, unlike UUIDv4;
* globally unique across tables, so the `/search` union can use `(score, id)` as a total order;
* time-sortable, which helps debugging and archiving.

JSON carries ids as **decimal strings**, because 2^63 is beyond JavaScript `Number` precision.

### Partitioning
* `books` and `authors` are hash-partitioned by `id`: writes are spread evenly and lookups by id
  prune to a single partition. Each partition is a future shard unit: Citus `create_distributed_table`
  on the same key, or manual `postgres_fdw`/application routing by `hash(id) % N`.
* The book↔author relation is **stored twice**, and each copy is partitioned by its own lookup key.
  `book_authors` is co-located with `books` (by book_id) and `author_books` is co-located with
  `authors` (by author_id). Both lookup directions are single-partition, so on a sharded cluster both
  are single-shard. The cost is double writes, done in the same transaction by the service.
  At 10B scale this beats one table with a secondary index, because that index would be spread over
  every shard.
* `audit_log` is range-partitioned by month. Old months can be detached and archived to cheap storage
  without vacuum storms. `ensure_audit_partitions()` creates partitions ahead of time; in production
  run it from cron or use pg_partman. A DEFAULT partition catches anything outside the range.
* 64/32/16 partitions are the modulus for the *current* node. Hash partitions can be split later by
  re-partitioning one partition at a time (attach a sub-partitioned table), or the modulus can be fixed
  at 256 from day one on real hardware. The trade-off is the next point.

**Measured cost of many partitions.** Every list query without the partition key (for example
"newest books") is a Merge Append over 64 partitions, and planning it opens 64 × 10 index relcache
entries. The pool therefore warms every connection on creation (`EXPLAIN` over each partitioned table).
Plans are always custom (`SET LOCAL plan_cache_mode = force_custom_plan`), because generic plans can
neither prune partitions at plan time nor estimate FTS selectivity, and
`LIMIT` is inlined as a literal for the same reason. See §7 for the virtiofs finding.

### Global uniqueness of ISBN
On a partitioned table a UNIQUE index must include the partition key. `UNIQUE(isbn_13)` on
`books(id)` is therefore impossible. Instead `book_isbns(isbn_13 PK)` is a registry partitioned by
the ISBN itself. Creating a book (or changing its ISBN) does `INSERT … ON CONFLICT DO NOTHING` in the
same transaction, and 0 rows means 409. A soft delete releases the ISBN. A side benefit: the
`isbn` filter becomes a single-partition probe instead of 64 index probes. When sharded, the registry
is sharded by ISBN. Uniqueness then needs a cross-shard transaction, either 2PC or the
"reserve ISBN, then insert book, compensate on failure" saga described in §6.

## 3. Indexes (and why there are not more)

All secondary indexes are **partial (`WHERE deleted_at IS NULL`)**. They are smaller, and the soft-delete
filter costs nothing because every list query already has it.

| index | serves |
|---|---|
| books `(title, id)` | sort=title (±), keyset |
| books `(coalesce(publication_year, 32767), id)` | sort=publication_year, year_from/year_to ranges |
| books `(created_at, id)` | default sort, created_from/created_to |
| books `(language_code)`, `(publisher)`, `(created_by)` | equality filters (bitmap-AND with others) |
| books GIN `(genres)` | genre filter `genres @> ARRAY[...]` |
| books GIN `(search_vector)` | q (full-text) |
| books GIN `(f_unaccent(title) gin_trgm_ops)` | title substring/fuzzy filter, `/search` typo tolerance |
| authors `(full_name, id)`, `(coalesce(birth_date,'infinity'), id)`, `(created_at, id)` | sorts, born ranges |
| authors `(country_code)`, GIN search_vector, GIN trigram on name | filters/search |
| PKs of book_authors/author_books | both relation directions + author-books keyset |
| book_isbns PK | ISBN lookup and uniqueness |
| audit_log `(entity_type, entity_id, changed_at, id)` | history keyset |

Choices:
* **No composite "filter + sort" indexes** such as `(language_code, created_at, id)`. With 13 filters × 3
  sorts that would be a zoo of indexes, and every one of them costs write amplification on a 10B-row
  table. Instead the planner either walks the sort index and filters (cheap when the filter is not
  selective) or bitmap-ANDs the equality indexes and sorts the few matching rows (when it is).
  `docs/QUERY_PLANS.md` shows both strategies picked correctly.
* NULLable sort keys are indexed through `coalesce(…)`, so keyset pagination stays one row-value
  range condition `(k, id) > ($1, $2)`. NULLs sort last ascending and first descending, the same as
  PostgreSQL's default.
* `isbn_13` has no index on `books`: the registry table is the index.
* `pg_trgm` + `btree_gin` are installed; `btree_gin` is available for future mixed GIN indexes, such as
  `(language_code, search_vector)`, if a language-scoped FTS hot spot appears.

## 4. Pagination
* Keyset only. The cursor is `base64url(JSON{s: sort key, v: last sort value, i: last id})`. It is
  validated on decode: wrong sort → 400 "restart pagination", malformed → 400. There is no OFFSET
  anywhere; `make explain` asserts it.
* Sorts are whitelisted (`domain/sorting.py`). Every sort has a `(key, id)` index and
  `ORDER BY key dir, id dir`, so ties are deterministic. Tests walk every sort with page sizes 1…100 over
  data with duplicated keys and NULLs and check that there are no duplicates and no gaps.
* `LIMIT limit+1` detects the next page without counting.
* `total` is computed only on request (`include_total=true`). It comes from
  `EXPLAIN (FORMAT JSON) SELECT 1 FROM … WHERE <same filters>` (the planner estimate), with
  `total_is_estimate: true`. `COUNT(*)` is never used.
* `/search` paginates by `(score, id)`. The score is computed in float8 and the cursor round-trips the
  exact double.
* History is ordered by `(changed_at, id)`; "books of an author" is ordered by `book_id DESC` (newest first).

## 5. Search
* Full-text: a generated `tsvector` with the `'simple'` configuration over `f_unaccent(text)`. It is
  language-agnostic (Cyrillic, Arabic, Latin with diacritics), and `f_unaccent` is an IMMUTABLE
  wrapper around `unaccent` with an explicit dictionary, so it can be used in generated columns and
  indexes. Queries use `websearch_to_tsquery`, which supports quotes, `OR` and `-`.
  The `author=` filter uses `to_tsquery` with prefix matching on the last word.
* Fuzzy: pg_trgm on `f_unaccent(title)` (and on author names) handles substring `ILIKE` (≥3 chars, which
  the API enforces) and typo-tolerant `<%` (word_similarity).
  CJK titles have no spaces, so they are matched by trigrams.
* `/search` relevance is `ts_rank + word_similarity`. Candidates come from the GIN indexes
  (FTS OR trigram) and are capped per type (`SEARCH_MAX_CANDIDATES`, newest ids first,
  deterministic) to bound the ranking cost.
* **External engine.** When search QPS, relevance tuning (stemming per language, synonyms, BM25,
  facets) or 10B-document FTS outgrow PostgreSQL, add OpenSearch/Elasticsearch as a *read model*:
  1. Run CDC with Debezium on logical replication (`wal_level=logical` is already set in our config)
     over `books`, `authors` and `book_authors`, or use a transactional `outbox` table written in
     the same transaction as the change.
  2. A consumer builds denormalised documents (book plus author names), sends them to the index with
     `version` as the external version (idempotent and ordered), and deletes them on soft delete.
  3. `/search` and `q=` are served by OpenSearch, which returns ids and `search_after`
     (a keyset-equivalent cursor); cards still come from PostgreSQL by id. The API contract is unchanged.

## 6. Writes, consistency and audit
* Each write is one transaction: lock the row (`SELECT … FOR UPDATE`), compare `version` with
  `If-Match` (or body `version`), apply, `version + 1`, set `updated_at/updated_by`, maintain the ISBN
  registry and both link tables, and append to `audit_log`. A missing version gives 428 and a stale
  one gives 409.
* `audit_log.diff` holds the full snapshot for create, `{field: {old, new}}` for update, and the
  `deleted_at` value for delete. Together with `created_by/created_at` and `updated_by/updated_at` on
  every row, it answers "who changed what and when". `GET …/history` reads it with keyset pagination.
* Bulk load uses binary COPY into the partitioned parents. The seed drops secondary indexes and FKs
  first, then rebuilds them in parallel and runs `VACUUM ANALYZE`.
* Sharded future: FKs between shards disappear; the service already validates authors explicitly. The
  dual relation write and the ISBN registry would use an outbox/saga per shard pair.

## 7. Operations
* Every transaction starts with `BEGIN …; SET LOCAL statement_timeout; SET LOCAL lock_timeout;
  SET LOCAL plan_cache_mode` in **one round trip**. Nothing is session-scoped, so the pool can run
  behind PgBouncer in transaction mode. Set `DB_STATEMENT_CACHE_SIZE=0` for PgBouncer < 1.21.
* Every list has `LIMIT ≤ 100` (validated: >100 → 422), and every query has a statement timeout
  (→ 503 problem).
* Observability: JSON logs with request id, route, status and duration; `/healthz` for liveness;
  `/readyz` pings the DB; Prometheus `/metrics` with per-route counters and latency histograms
  (multiprocess mode across uvicorn workers).
* Docker: multi-stage uv build, non-root uid 10001, read-only root fs plus tmpfs `/tmp`, HEALTHCHECK.

**Finding:** on Docker Desktop for macOS, PGDATA on a **bind mount** (virtiofs) cut API throughput by
roughly 50× (10 rps, multi-second planning). Planning over many partitions issues per-relation file-size
syscalls, and on virtiofs each one is a host round trip that serialises under concurrency. A named
volume or tmpfs gives 560 rps with the same code.

## 8. Known limitations
* Single-node demo: 64/32 partitions are already more than 1M rows need. Planning a non-pruned list
  query costs ~1–5 ms of CPU. A sharded deployment spreads the partitions out.
* Relevance is computed over at most `SEARCH_MAX_CANDIDATES` matches per type (the newest), so it is
  not a global top-N for very common terms. That is the trigger to move search to OpenSearch.
* `author=` (name) on very prolific name tokens can match thousands of authors; it is bounded by
  statement_timeout.
* `title`/`name` substring filters require ≥ 3 characters (trigram index limitation).
* Seeded rows have a single synthetic "create" audit entry.
* Auth is a trusted header (by design of the task). Swap in a JWT `Authenticator` for production.
