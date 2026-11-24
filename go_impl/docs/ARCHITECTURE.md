# World Library — architecture

Target scale: 10+ billion books, hundreds of millions – billions of authors, tens of billions of
book↔author links. The demo runs 1M books / 200k authors on one PostgreSQL 16 node; every decision
below is made for the target scale and explains what changes when we get there.

## 1. Components

```
client ──HTTP──> app (Go, chi)  ──pgx pool──> [PgBouncer, transaction mode] ──> PostgreSQL 16
                  handlers → service → repository          (pg_trgm, btree_gin, unaccent)
                  /metrics /healthz /readyz /docs
seed  (same binary, `library seed`)   ── COPY ──> PostgreSQL
migrate (same binary, `library migrate`, goose, embedded SQL)
```

* One static binary (`cmd/library`) with sub-commands `serve | migrate | seed | explain | healthcheck`;
  distroless non-root image (~16 MB).
* Layers: `internal/httpapi` (transport, RFC 7807, middleware) → `internal/service` (use cases,
  optimistic locking, audit, transactions) → `internal/repository` (SQL via pgx + squirrel).
  `internal/domain` holds entities/validation/query parsing; no globals (metrics use a private
  Prometheus registry, config is passed explicitly).
* Auth is an `Authenticator` interface; the current `HeaderAuth` trusts `X-User-Id` and checks the user
  exists. A JWT implementation drops in without touching handlers.

## 2. Data model

| table | partitioning | key | notes |
|---|---|---|---|
| `users` | none (small) | `id` | `username`, `email` unique; id 1 = `system` |
| `authors` | HASH(`id`), 32 | `id` | `search_vector` generated (full_name A + original_name A), soft delete, `version` |
| `books` | HASH(`id`), 64 | `id` | `search_vector` generated (title A, original_title A, subtitle B), `genres text[]`, `external_ids jsonb`, soft delete, `version` |
| `book_authors` | HASH(`book_id`), 64 | (`book_id`,`author_id`,`role`) | "authors of a book"; co-located with `books` (same key & modulus) |
| `author_books` | HASH(`author_id`), 64 | (`author_id`,`book_id`,`role`) | "books of an author"; mirror written in the same transaction |
| `book_isbns` | HASH(`isbn_13`), 32 | `isbn_13` | global ISBN-13 uniqueness + ISBN lookup |
| `audit_log` | RANGE(`changed_at`) monthly + DEFAULT | (`changed_at`,`id`) | who/when/what (`diff jsonb`) |

Integrity: CHECKs on language (`^[a-z]{2,3}$`), country (`^[A-Z]{2}$`), ISBN formats, year
(-3000..2100), dates (death ≥ birth), lengths, role enum, `jsonb_typeof(external_ids)='object'`;
FKs `created_by/updated_by → users`, links → books/authors. Check digits are validated in the
application (`internal/isbn`), ISBN-10 is converted to ISBN-13 so only one lookup path exists.

**Why ISBN uniqueness lives in `book_isbns`.** A unique index on a partitioned table must include the
partition key, so `UNIQUE(isbn_13)` on `books` (hashed by `id`) is impossible. `book_isbns` is a
"global index table" partitioned by the ISBN itself: its PK enforces global uniqueness, it answers
`?isbn=` with a single-partition lookup, and it shards the same way as everything else (by its own
key). It is maintained in the same transaction as the book (insert on create, delete/insert on change,
delete on soft delete so the ISBN can be reused).

**Ids.** 63-bit snowflake (`41 bits ms | 10 bits node | 12 bits seq`, epoch 2020-01-01), serialized
as JSON strings (they exceed 2^53). Time-ordered → B-tree inserts are append-mostly (like UUIDv7 but
8 bytes instead of 16). Node ids 0..1000 are for app instances, 1001+ are reserved for the seeder.
Ids are globally unique across tables, which `search` uses as a tie-breaker in a UNION.

**Audit.** Every write runs in one transaction: row change + `audit_log` insert with
`diff = {"field": {"old": …, "new": …}}` and `changed_by` from the authenticated user. Rows also
carry `created_at/by`, `updated_at/by` (`updated_* = created_*` on insert), so "who and when" is
visible on every card and list item; `GET /books/{id}/history` / `/authors/{id}/history` read the
log (newest first, keyset). Monthly partitions are created by `ensure_audit_partitions()` (migrate
creates 13 months ahead; run it from cron in production); old months can be detached/archived
cheaply. The history index is `(entity_type, entity_id, changed_at DESC, id DESC)` per month; at
10B scale history lookups touch one index per month — acceptable, or add a hash-partitioned
`(entity_id)` copy if history becomes hot.

## 3. Partitioning and the path to sharding

* `books`, `book_authors`: HASH by book id with the **same modulus (64)** → a book and its author
  links live in partitions with the same remainder; when partitions move to shards (Citus
  `create_distributed_table(..., colocate_with)` or manual shard map `remainder → node`) the
  book card stays single-shard.
* `author_books`: HASH by author id → "books of an author" is a single-partition (single-shard)
  index range scan. The price is a second copy of the links (≈ 80 MB per 1M books here), written
  in the same transaction. Across shards this becomes a 2-shard write — use Citus 2PC or an
  outbox/CDC job that rebuilds `author_books` asynchronously.
* `authors`: HASH by id (32). `book_isbns`: HASH by ISBN (32).
* The API never exposes partitioning: ids are opaque, cursors are opaque, so moving from
  partitions to shards does not change the API.
* 64 partitions is the floor required by the spec; at 10B rows that is ~150M rows/partition —
  production would use 256–1024 partitions (or Citus shards) so each partition stays < 50–100 GB
  (vacuum, index builds, `pg_repack` stay manageable).

**Known cost: planning.** Every list query without a partition key touches all 64 partitions
(Merge Append of 64 index scans). Execution is < 1 ms, but planning is ~4–5 ms (see
`docs/QUERY_PLANS.md`). Mitigations, in order: prepared statements with `plan_cache_mode = auto`
(pgx caches statements per connection; PostgreSQL switches to generic plans), fewer but larger
partitions per node with sharding across nodes (Citus plans per shard in parallel), or a search
engine for "browse everything" listings.

## 4. Indexes (and why there are not more)

All list indexes are **partial** `WHERE deleted_at IS NULL` (soft-deleted rows never bloat hot
indexes) and **end with `id`** (keyset tie-breaker).

| index | serves |
|---|---|
| `books (title, id)` | `sort=±title` |
| `books (publication_year, id)` | `sort=±publication_year`, `year_from/to` |
| `books (created_at, id)` | default `sort=-created_at`, `created_from/to` |
| `books (language_code, title, id)` | `language=` + `sort=title` (added after EXPLAIN showed scripts sort far apart → 10k rows filtered per partition) |
| `books (language_code, publication_year, id)` | `language=` + year range / year sort |
| `books (publisher)` | `publisher=` |
| `books (created_by, created_at, id)` | `created_by=` ("what did user X add") |
| `books GIN (genres)` | `genre=` (array containment) |
| `books GIN (search_vector)` | `q=` full text |
| `books GIN (norm_text(title ‖ original_title) gin_trgm_ops)` | `title=` substring/fuzzy, search typo tolerance |
| `authors (full_name,id)`, `(birth_date,id)`, `(created_at,id)` | author sorts / ranges |
| `authors (country_code, full_name, id)` | `country=` (+ name sort) |
| `authors GIN (search_vector)`, `GIN trgm(name)` | `q=`, `name=`, search |
| `author_books PK`, `book_authors PK`, `book_isbns PK` | link directions, ISBN lookup |
| `audit_log (entity_type, entity_id, changed_at DESC, id DESC)` | history |

Deliberately **not** indexed: every (filter × sort) combination (write amplification on 10B rows:
each extra index ≈ +30–60 bytes/row and one more random write per insert), `isbn_13` on `books`
(served by `book_isbns`), `description` (not searched; it would triple the GIN size). One `btree_gin`
multi-column GIN over all facets was considered: it handles arbitrary equality combinations but
cannot return rows in sort order, so it loses to the sort indexes for `LIMIT 20` pages; it remains
an option for analytics-style facet counts. Combinations that are not covered still never Seq Scan
(they walk a sort index and filter), but can be slow for rare values; `statement_timeout` caps them
and they are the first candidates for the search engine.

Measured sizes at 1M books: books heap 302 MB + indexes 392 MB (largest: trigram 79 MB,
lang+title 56 MB, title 52 MB).

## 5. Pagination

* Keyset only. Cursor = base64url(JSON `{s: sort, v: last sort value | null, i: last id}`); it is bound
  to the sort (a cursor from `sort=title` is rejected for `sort=-title`) and validated on decode.
* Predicate `(col, id) > ($v, $id)` (or `<` for DESC) — a row comparison PostgreSQL uses as an index
  range condition, so page N costs the same as page 1. `LIMIT n+1` detects the next page; no
  `OFFSET` is ever generated (unit tests and `make explain` assert it).
* Nullable sort fields (`publication_year`, `birth_date`) put NULLs **last in both directions** using
  two phases: phase 1 `col IS NOT NULL` + row comparison, phase 2 `col IS NULL AND id >/< $id`; both
  are served by the same `(col, id)` index. A cursor with `v = null` means "inside the NULL tail".
* `total` is not computed by default; `include_total=true` returns the planner estimate
  (`EXPLAIN (FORMAT JSON)` of the same WHERE → `Plan Rows`) with `total_is_estimate: true`. Never
  `COUNT(*)`.
* Hard limits: `limit ≤ 100`, `statement_timeout` (5 s, startup parameter), per-request context
  timeout (10 s).

## 6. Search

* Full text: generated `tsvector` with the `simple_unaccent` configuration (`simple` dictionary +
  `unaccent`) — language-agnostic (Latin, Cyrillic, Arabic, …), no stemming, so it never mangles
  foreign words; queries use `websearch_to_tsquery` (quotes, `OR`, `-word`).
* Fuzzy/substring: `pg_trgm` GIN over `norm_text(title ‖ original_title)` (= `lower(unaccent())`,
  wrapped as IMMUTABLE). Substring filters need ≥ 3 characters (one full trigram), otherwise 400.
* `/search`: candidates = FTS match OR trigram word-similarity (`<%`), both GIN (BitmapOr);
  `rank = ts_rank + word_similarity`; ordered by `(rank DESC, id DESC)` with keyset on the computed
  rank. Ranking needs all candidates, so very common words cost O(matches) — bounded by
  `statement_timeout`. Known limitation: CJK has no word boundaries in the `simple` parser — CJK
  titles are matched by trigrams (substring), not FTS.

**When to add OpenSearch/Elasticsearch.** When relevance search over 10B documents must be < 100 ms,
when we need stemming per language/ICU tokenization (CJK), facets with counts, or typo tolerance
beyond trigram similarity. Integration: PostgreSQL stays the source of truth; changes flow either
through a transactional **outbox** table (written in the same transaction as the audit row; a relay
publishes to Kafka → indexer) or **logical replication/CDC** (Debezium on a publication of `books`,
`authors`, `book_authors`). The indexer denormalizes a book with its authors into one document,
idempotently by `(id, version)`. `/search` and `q=` switch to the engine behind the same API; filters
on exact fields and keyset lists remain in PostgreSQL.

## 7. Writes

* Single writes: one transaction (`SELECT … FOR UPDATE` → version check → UPDATE … `version = version
  + 1` WHERE `version = $expected` → links/ISBN maintenance → audit insert). `If-Match: "<version>"`
  (or body `version`) is mandatory for PUT/PATCH (428 otherwise), mismatch → 409. PATCH = JSON merge
  patch (`null` clears a field).
* Bulk: `library seed` — TRUNCATE, snapshot + drop secondary indexes and FKs (saved in
  `seed_ddl_backup` so a crashed run is repaired by the next one), parallel `COPY` (8 workers,
  50k-row chunks), rebuild indexes in parallel, re-add FKs (validated), `VACUUM (ANALYZE)`.
  Generation is deterministic (row *i* depends only on `SEED_RANDOM` and *i*).
* PgBouncer (transaction mode): no session state is used — no `SET`, no advisory locks, no temp
  tables, no LISTEN; `DB_PGBOUNCER=true` switches pgx to `QueryExecModeExec` (no named prepared
  statements) and expects `statement_timeout` to be set on the role
  (`ALTER ROLE app SET statement_timeout = '5s'`).

## 8. Observability

JSON logs (`log/slog`) with request id, route, status, duration, user id; `/healthz` (liveness),
`/readyz` (DB ping), `/metrics` (`http_requests_total`, `http_request_duration_seconds{route}`,
pgx pool gauges, Go runtime). `pg_stat_statements` is preloaded in the PostgreSQL config.

## 9. Known limitations / next steps

* Planning overhead of 64 partitions dominates simple queries (§3).
* `GET /books?author=<name>` uses at most the first 1000 matching authors (documented in the spec).
* `/authors/{id}/books` is ordered by book id (≈ creation time) only; ordering by title/year would
  need those columns denormalized into `author_books`.
* Deleting an author keeps the link rows; the deleted author disappears from book cards.
* `audit_log` history is per-month partitioned → history lookups visit every month's index.
* Search relevance is basic (no per-language stemming); see §6 for the search-engine path.
* Cross-shard writes (`author_books` mirror, `book_isbns`) need 2PC or outbox once sharded.
* `make explain` verifies custom plans; generic (cached) plans are not checked separately.
