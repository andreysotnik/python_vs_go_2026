# World Library (Rust)

Backend for a catalog of all books and authors of the world, designed for 10B+ books on
PostgreSQL. Rust · axum · tokio · sqlx · serde · PostgreSQL 17 (pg_trgm, btree_gin, unaccent).

* Architecture, schema, partitioning, indexes, search, sharding path: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
* Query plans on 1M seeded books (custom + generic plans, no Seq Scan): [docs/QUERY_PLANS.md](docs/QUERY_PLANS.md)
* API: [openapi.yaml](openapi.yaml) (OpenAPI 3.1), Swagger UI at http://localhost:38003/docs
* Decisions and assumptions: [DECISIONS.md](DECISIONS.md), work log: [WORKLOG.md](WORKLOG.md)

## Quick start

Requirements: Docker with compose v2 (the Rust toolchain is only needed for `*-local` targets).

```bash
docker compose up --build -d     # postgres + migrations (one-shot service) + app on :38003
make seed                        # 1M books / 200k authors (SEED_BOOKS, SEED_AUTHORS, SEED_RANDOM)
make test                        # unit + integration tests (inside a Rust container)
make explain                     # EXPLAIN (ANALYZE, BUFFERS) check -> docs/QUERY_PLANS.md
make bench                       # k6 load test -> bench/results/k6-report.md
make lint                        # rustfmt --check + clippy -D warnings (local toolchain)
make down                        # stop (data volume kept)
```

| make target | what it does |
|---|---|
| `up` | `docker compose up -d --build` |
| `migrate` | runs the `migrate` service (`world-library migrate`, sqlx embedded migrations) |
| `seed` | builds, migrates, runs the `seed` service (profile `seed`) |
| `test` | `cargo test` in `rust:1.100` container against the compose PostgreSQL; each run creates its own database |
| `test-local` / `cover` | same with the local toolchain / coverage via `cargo llvm-cov` |
| `lint` | `cargo fmt --check` + `cargo clippy --all-targets -- -D warnings` |
| `explain` | plan checker on the seeded DB (fails on Seq Scan on big tables) |
| `bench` | `grafana/k6` container on the compose network, `VUS=32 DURATION=60s` by default |

Host ports: PostgreSQL `35432`, app `38003`. Compose project: `lib_rust`.

## Configuration (environment variables)

| variable | default | |
|---|---|---|
| `DATABASE_URL` | — (required) | `postgres://library:library@postgres:5432/library` in compose |
| `HTTP_ADDR` | `0.0.0.0:8080` | |
| `DB_MAX_CONNECTIONS` / `DB_MIN_CONNECTIONS` | `32` / `2` | pool size |
| `DB_ACQUIRE_TIMEOUT_MS` | `3000` | pool exhaustion → 503 |
| `DB_STATEMENT_TIMEOUT_MS` | `5000` | per transaction (`set_config(..., true)`), timeout → 503 |
| `DB_STATEMENT_CACHE_CAPACITY` | `256` | 0 for PgBouncer < 1.21 |
| `REQUEST_TIMEOUT_MS` | `15000` | → 504 |
| `NODE_ID` | `1` | snowflake node id (0..1023), unique per replica |
| `DEFAULT_PAGE_SIZE` | `20` | max page size is 100 |
| `LOG_LEVEL` | `info` | tracing EnvFilter syntax, JSON logs |
| `SEED_BOOKS` / `SEED_AUTHORS` / `SEED_USERS` | `1000000` / `200000` / `1000` | seed volume |
| `SEED_RANDOM` | `42` | deterministic data (same seed → identical rows and ids) |
| `SEED_WORKERS` / `SEED_BATCH_SIZE` | `8` / `25000` (compose) | parallel COPY |
| `SEED_TRUNCATE` | `true` | seed replaces catalog data |

## API examples

```bash
API=http://localhost:38003/api/v1

# create a user (actor) — or use the bootstrap user "1"
curl -s -XPOST $API/users -H 'Content-Type: application/json' \
  -d '{"username":"librarian","email":"librarian@example.org"}'

# create an author and a book (writes need X-User-Id)
AUTHOR=$(curl -s -XPOST $API/authors -H 'X-User-Id: 1' -H 'Content-Type: application/json' \
  -d '{"full_name":"Leo Tolstoy","original_name":"Лев Толстой","birth_date":"1828-09-09","country_code":"RU"}' | jq -r .id)
BOOK=$(curl -s -XPOST $API/books -H 'X-User-Id: 1' -H 'Content-Type: application/json' \
  -d "{\"title\":\"War and Peace\",\"original_title\":\"Война и мир\",\"language_code\":\"en\",
       \"isbn_10\":\"0-306-40615-2\",\"publication_year\":1869,\"genres\":[\"novel\"],
       \"authors\":[{\"author_id\":\"$AUTHOR\",\"role\":\"author\"}]}" | jq -r .id)

# card (ETag = version), optimistic update, history
curl -si $API/books/$BOOK | grep -i etag                     # ETag: "1"
curl -s -XPATCH $API/books/$BOOK -H 'X-User-Id: 1' -H 'If-Match: "1"' \
  -H 'Content-Type: application/json' -d '{"subtitle":"A Novel"}' | jq .version   # 2
curl -s -XPATCH $API/books/$BOOK -H 'X-User-Id: 1' -H 'If-Match: "1"' \
  -H 'Content-Type: application/json' -d '{"subtitle":"x"}' | jq .status          # 409
curl -s $API/books/$BOOK/history | jq '.items[] | {action, changed_by, changed_at, diff}'

# lists: filters + whitelisted sort + keyset cursor + estimated total
curl -s "$API/books?language=ru&genre=novel&year_from=1850&year_to=1900&sort=-publication_year&limit=20&with_total=true"
curl -s "$API/books?sort=title&limit=20&cursor=<next_cursor from the previous page>"
curl -s "$API/books?q=war%20peace"            # full text (websearch syntax)
curl -s "$API/books?title=peace&author=tolst" # substring / fuzzy (trigram)
curl -s "$API/books?isbn=978-0-306-40615-7"
curl -s "$API/authors?country=RU&born_from=1800-01-01&sort=birth_date"
curl -s "$API/authors/$AUTHOR/books?limit=50"
curl -s "$API/search?q=tolstoi&type=all"      # typo -> fuzzy mode
curl -s -XDELETE $API/books/$BOOK -H 'X-User-Id: 1'          # soft delete, 204
```

List response: `{"items": [...], "next_cursor": "…"|null, "limit": 20, "total": 123|null, "total_is_estimate": true|false}`.
Errors: RFC 7807 `application/problem+json` (`400` malformed request/cursor/param, `401` missing or
unknown `X-User-Id`, `404`, `409` version/ISBN conflict, `422` validation with `errors[]`,
`428` missing If-Match/version, `503` DB timeout/unavailable).

## Results (this machine: macOS arm64, 12 CPU, Docker Desktop; all containers on one host)

* Tests: 72 passed (57 unit + 14 API integration + 1 seed/explain end-to-end), 0 failed;
  line coverage 93.9% overall (`cargo llvm-cov`), service 94.9%, query builders 99.7%,
  validation 96.4%, repository 100%.
* Seed 1M books / 200k authors / 1k users (5.87M rows incl. links, ISBN registry, audit):
  **33.0 s total**; COPY phase 8.0 s = **732k rows/s** (books 166k/s), parallel index + FK rebuild
  9.1 s, VACUUM ANALYZE 15.6 s. Log: `bench/results/seed.log`.
* Plan check: 29 key queries, no Seq Scan on big tables in custom or generic plans,
  most list queries 1–5 ms on 1M books ([docs/QUERY_PLANS.md](docs/QUERY_PLANS.md)).
* k6, 32 VUs, 60 s, mixed read/write: 658 req/s, 0% errors, overall p50 36 ms / p95 160 ms /
  p99 260 ms ([bench/results/k6-report.md](bench/results/k6-report.md)); the heaviest endpoint is
  `/search` (p95 293 ms), list endpoints p95 76–94 ms, card p95 32 ms. k6, app and PostgreSQL
  share the same 12 cores.

## Extrapolation to 10B books

Measured on the seeded DB (per 1M books, 1.27 links per book, 200k authors):

| relation | heap | indexes | per 1M books | ×10 000 (10B books) |
|---|---:|---:|---:|---:|
| books | 315 MB | 287 MB | 0.63 GB | ~6.3 TB |
| book_authors + author_books | 156 MB | 147 MB | 0.32 GB | ~3.2 TB |
| isbn_registry | 47 MB | 36 MB | 0.09 GB | ~0.9 TB |
| authors (200k → 1B authors) | 45 MB | 58 MB | 0.11 GB | ~0.5 TB |
| audit_log (1 entry per entity) | 120 MB | 119 MB | 0.25 GB | ~2.5 TB (grows with edits; old months detachable) |
| **total** | | | **1.36 GB** | **~13–14 TB** per copy (+WAL, replicas ×2–3) |

* Indexes are ~48% of `books`; the largest are the trigram GIN (60 MB/1M) and the title B-trees.
* Load time: COPY scales linearly with partition-parallel workers: 58.7B rows at the measured
  ~730k rows/s of one node ≈ 22 h of COPY plus index builds (N log N, ≈ 9 s per 1M books here →
  1–2 days on one node). Sharded across 16 nodes (Citus / per-shard loaders, each with 8+ workers)
  the bulk load is ≈ 2–4 h plus index builds in parallel per shard. Real hardware with NVMe and
  more `maintenance_work_mem` changes constants, not the shape.
* Per partition at 10B with 256 partitions: ~40M books ≈ 25 GB heap+indexes — comfortable for
  VACUUM, index rebuilds and moving partitions to shards.
* Keyset queries stay O(limit × partitions) regardless of size; the cost that grows is the
  MergeAppend fan-out (64–256 index probes per page) — at that point the sharding layer routes
  sorted pages per shard and merges (Citus does this), or a dedicated sorted projection is kept.
