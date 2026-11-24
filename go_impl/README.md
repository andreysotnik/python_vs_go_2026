# World Library — Go backend

Catalog of all books and authors of the world: REST API (Go 1.26, chi, pgx, squirrel, goose) on
PostgreSQL 16 (pg_trgm, btree_gin, unaccent), designed for 10+ billion books. Architecture and the
reasoning behind every index/partition: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). Verified query
plans: [docs/QUERY_PLANS.md](docs/QUERY_PLANS.md). Decisions & assumptions: [DECISIONS.md](DECISIONS.md).

## Quick start

```bash
docker compose up --build        # postgres → migrate (one-shot) → app on http://localhost:28002
make seed                        # 1M books / 200k authors (~70 s), deterministic
make test                        # unit + integration tests against the compose postgres (+coverage)
make explain                     # EXPLAIN ANALYZE key queries on the seeded DB → docs/QUERY_PLANS.md
make bench                       # k6 load test → loadtest/results/summary.md
make lint                        # golangci-lint v2 in Docker
make down
```

* Swagger UI: http://localhost:28002/docs · spec: http://localhost:28002/openapi.yaml
  (file: [api/openapi.yaml](api/openapi.yaml), OpenAPI 3.1; a test checks that every routed endpoint is
  documented and vice versa).
* Ops: `/healthz` (liveness), `/readyz` (DB), `/metrics` (Prometheus).
* Host ports: PostgreSQL `25432`, app `28002`. Compose project name `lib_go`.
* Everything runs in containers; a local Go toolchain is optional (`make test-local`).

## Configuration (environment variables)

| variable | default | meaning |
|---|---|---|
| `DATABASE_URL` | `postgres://library:library@localhost:25432/library?sslmode=disable` | connection string |
| `HTTP_ADDR` | `:8080` | listen address |
| `DB_MAX_CONNS` / `DB_MIN_CONNS` | `32` / `4` | pgx pool size |
| `DB_STATEMENT_TIMEOUT` | `5s` | `statement_timeout` (sent as startup parameter) |
| `DB_PGBOUNCER` | `false` | PgBouncer transaction mode: no named prepared statements, no session params (set `statement_timeout` on the role) |
| `REQUEST_TIMEOUT` | `10s` | per-request context deadline |
| `SHUTDOWN_TIMEOUT` | `15s` | graceful shutdown |
| `NODE_ID` | `1` | snowflake node id (0..1000, unique per instance) |
| `LOG_LEVEL` | `info` | `debug/info/warn/error` (JSON logs to stderr) |
| `SEED_BOOKS` / `SEED_AUTHORS` / `SEED_USERS` | `1000000` / `200000` / `100` | seed volume |
| `SEED_RANDOM` | `42` | seed of the deterministic generator |
| `SEED_WORKERS` / `SEED_BATCH` | `8` / `50000` | parallel COPY streams / rows per COPY chunk |

Example: `SEED_BOOKS=100000 SEED_AUTHORS=20000 make seed`.

## API examples

```bash
API=http://localhost:28002/api/v1
# a user (the acting user for writes is passed in X-User-Id)
U=$(curl -s -XPOST $API/users -H 'Content-Type: application/json' \
      -d '{"username":"librarian","email":"lib@example.org"}' | jq -r .id)

# author + book
A=$(curl -s -XPOST $API/authors -H "X-User-Id: $U" -H 'Content-Type: application/json' \
      -d '{"full_name":"Lev Tolstoy","original_name":"Лев Толстой","birth_date":"1828-09-09","country_code":"RU"}' | jq -r .id)
B=$(curl -s -XPOST $API/books -H "X-User-Id: $U" -H 'Content-Type: application/json' \
      -d "{\"title\":\"Война и мир\",\"original_title\":\"War and Peace\",\"language_code\":\"ru\",\"isbn_10\":\"0-306-40615-2\",
           \"publication_year\":1869,\"genres\":[\"classics\"],\"authors\":[{\"author_id\":\"$A\"}]}" | jq -r .id)

# optimistic locking: If-Match = current version (ETag); stale → 409, missing → 428
curl -s -XPATCH $API/books/$B -H "X-User-Id: $U" -H 'If-Match: "1"' -H 'Content-Type: application/json' -d '{"subtitle":"Том 1"}'
curl -s $API/books/$B/history | jq '.items[] | {action, changed_by, changed_at, diff}'

# lists: filters + whitelisted sort + keyset cursor (+ estimated total)
curl -s "$API/books?language=ru&year_from=1800&year_to=1900&sort=title&limit=20&include_total=true"
curl -s "$API/books?sort=title&limit=20&cursor=<next_cursor from previous page>"
curl -s "$API/books?q=war%20OR%20peace"          # full-text (websearch syntax)
curl -s "$API/books?title=miserables"            # fuzzy substring, accent-insensitive
curl -s "$API/books?isbn=978-0-306-40615-7"
curl -s "$API/authors?country=RU&sort=-birth_date"
curl -s "$API/authors/$A/books"
curl -s "$API/search?q=tolstoi&type=all"         # relevance: ts_rank + trigram similarity

# soft delete (If-Match optional)
curl -s -XDELETE $API/books/$B -H "X-User-Id: $U"
```

List envelope: `{"items":[…],"next_cursor":"…"|null,"limit":20,"total":null|N,"total_is_estimate":false|true}`.
Errors: RFC 7807 `application/problem+json` with `errors: [{field, message}]` (400 malformed request,
401 unknown user, 404, 409 version/duplicate conflict, 422 validation, 428 missing If-Match, 504 timeout).

## Tests

* Unit: validation, ISBN generation/check digits, cursor encode/decode, SQL/keyset builders (no OFFSET,
  row comparison, NULL phases), error mapping, config, deterministic generator.
* Integration (real PostgreSQL, `internal/integration`, own databases created/dropped per run): CRUD,
  audit (created_by/updated_by/history with diffs), keyset pagination over every sort with limits
  1/2/3 vs one big page (no duplicates/gaps, tie-break on equal values, NULLs last), every filter,
  FTS/fuzzy/multiscript search, optimistic locking incl. concurrent PATCHes, soft delete, 400/401/404/409/422/428,
  OpenAPI ↔ router consistency, seed (small volume, idempotent, indexes/FKs restored) + EXPLAIN tool.
* `make explain`: EXPLAIN (ANALYZE, BUFFERS) for 40 key queries (incl. batch lookups for a real 100-row page) on the seeded DB; fails if any plan has
  a Seq Scan on books/authors/links/isbn/audit (incl. partitions) or SQL contains OFFSET.
* `make bench`: k6, mixed read workload over 11 endpoint types, p50/p95/p99 per endpoint.

## Load test results (k6, seeded DB: 1M books / 200k authors)

Mixed read workload (`loadtest/load.js`): cards, lists with every sort, facet filters, a deep page,
full-text, fuzzy title, author's books, global search, history. The host was shared with another
heavy workload (load average 20–90 on 12 CPUs), so absolute numbers are pessimistic.

**8 VUs (45 s)**
VUs: 8, duration: 45s, requests: 14757, RPS: 321, failed: 0.00%

| endpoint | p50 ms | p95 ms | p99 ms | max ms |
|---|---:|---:|---:|---:|
| **all** | 5.1 | 128.7 | 442.6 | 785.3 |
| author_books | 10.0 | 15.7 | 19.6 | 66.7 |
| author_get | 0.7 | 1.1 | 2.2 | 5.2 |
| authors_list | 1.2 | 2.1 | 3.6 | 14.1 |
| book_get | 1.4 | 2.6 | 4.6 | 23.3 |
| book_history | 1.6 | 2.7 | 4.3 | 16.0 |
| books_filter | 14.2 | 19.6 | 24.6 | 78.2 |
| books_fts | 12.4 | 17.0 | 19.9 | 48.6 |
| books_fuzzy | 97.7 | 158.5 | 178.5 | 213.1 |
| books_list | 5.7 | 8.2 | 10.1 | 21.9 |
| books_page2 | 5.7 | 8.7 | 11.7 | 25.2 |
| search | 145.3 | 600.1 | 654.9 | 785.3 |

**32 VUs (60 s, `make bench`)**
VUs: 32, duration: 60s, requests: 22333, RPS: 365, failed: 0.00%

| endpoint | p50 ms | p95 ms | p99 ms | max ms |
|---|---:|---:|---:|---:|
| **all** | 13.4 | 507.8 | 1526.0 | 3359.9 |
| author_books | 27.0 | 73.7 | 120.9 | 956.6 |
| author_get | 1.1 | 8.7 | 14.5 | 48.6 |
| authors_list | 2.2 | 16.6 | 37.9 | 169.1 |
| book_get | 3.6 | 17.2 | 31.1 | 80.3 |
| book_history | 4.3 | 30.9 | 77.1 | 224.4 |
| books_filter | 48.0 | 99.2 | 130.5 | 989.7 |
| books_fts | 40.6 | 86.1 | 125.4 | 833.5 |
| books_fuzzy | 325.6 | 763.2 | 939.0 | 1280.8 |
| books_list | 14.3 | 49.4 | 86.6 | 1129.7 |
| books_page2 | 14.8 | 54.0 | 97.6 | 151.1 |
| search | 497.6 | 1980.6 | 2633.3 | 3359.9 |

Interpretation: point lookups and keyset lists stay in single-digit milliseconds; the cost is
dominated by planning over 64 partitions (~4–5 ms, see ARCHITECTURE §3). `search` and fuzzy
`title=` rank/scan all trigram/FTS candidates for common words and saturate CPU at 32 VUs — the
documented trigger for moving relevance search to OpenSearch.

## Seeding & extrapolation to 10 billion books

Measured on this machine (Docker VM: 12 vCPU, 7.6 GB RAM, shared with other workloads), 1M books /
200k authors / 100 users: **5.96M rows COPY in 9.0 s (≈ 660k rows/s)**, index rebuild + FK validation
33 s, VACUUM ANALYZE 25 s, **total ≈ 68 s**. Rows per book ≈ 6 (book, ~1.32 links × 2 directions,
0.92 ISBN, audit).

On-disk size at 1M books: books 694 MB (heap 302 + indexes 392), audit 311 MB, links 2 × ~158 MB,
ISBN 84 MB, authors 101 MB (200k) → **≈ 1.5 GB per million books** with mock data (short titles,
50 % short descriptions).

| | 1M (measured) | 10B (extrapolated) |
|---|---|---|
| data + indexes | 1.5 GB | ≈ 15 TB with mock-sized rows; ≈ 30–40 TB with real descriptions/metadata; ×(1 + replicas) |
| rows to load | 6M | 60B |
| COPY, single node @ 660k rows/s | 9 s | ≈ 25 h |
| index build + FK + analyze | 58 s | ≈ 4–7 days on one node (sorts spill to disk beyond RAM) |
| sharded: 32 nodes, parallel load | — | COPY < 1 h, indexes ≈ 4–6 h per shard in parallel |
| largest single index (title trigram) | 79 MB | ≈ 0.8 TB → must be split across shards |

Practical notes for that scale: load per partition/shard in parallel (the generator is deterministic
by row index, so each loader takes a disjoint index range), build indexes per partition
(`CREATE INDEX … ON ONLY` + per-partition `CONCURRENTLY` + `ATTACH`), keep `max_wal_size` large or
load with `wal_level=minimal` before enabling replication, and raise partitions to 256–1024 so a
partition stays < 100 GB. FKs on links become impractical across shards — validate in the loader.
