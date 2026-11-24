# World Library API (Python 3.12 / FastAPI / PostgreSQL 16)

A catalogue of every book and author in the world, designed for 10B+ books. It provides keyset pagination,
full-text and fuzzy search, an audit trail and optimistic locking.
The design is described in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), the query plans in
[docs/QUERY_PLANS.md](docs/QUERY_PLANS.md) and the decisions in [DECISIONS.md](DECISIONS.md).

## Quick start

```bash
docker compose up --build -d     # postgres (tuned) -> migrate (alembic, one-shot) -> app on :18001
make seed                        # 1M books / 200k authors via COPY (SEED_BOOKS / SEED_AUTHORS / SEED_RANDOM)
make test                        # unit + integration (testcontainers PostgreSQL), coverage gate 80%
make lint                        # ruff + ruff format --check + mypy --strict
make explain                     # EXPLAIN (ANALYZE, BUFFERS) checks on the seeded DB -> docs/QUERY_PLANS.md
make bench                       # k6 load test (docker image grafana/k6) -> bench/results/
make down
```

* Swagger UI: http://localhost:18001/docs. OpenAPI 3.1: http://localhost:18001/openapi.yaml (also
  `openapi.yaml` in the repo; a test fails if it drifts from the code; regenerate with `make openapi`).
* Health: `/healthz` (liveness), `/readyz` (DB), metrics: `/metrics` (Prometheus).
* Host ports: PostgreSQL `15432`, API `18001`. Compose project name: `lib_python`.
* `make test`/`make lint` need [uv](https://docs.astral.sh/uv/) on the host. Everything else needs only Docker.
* Constrained Docker Desktop (VM disk full, little RAM): run with
  `COMPOSE_FILE=docker-compose.yml:deploy/compose.devbox.yml`, which keeps PGDATA on tmpfs. See DECISIONS.md.

## Configuration (environment variables)

| variable | default | meaning |
|---|---|---|
| `DATABASE_URL` | `postgresql://library:library@localhost:15432/library` | PostgreSQL DSN |
| `DB_POOL_MIN` / `DB_POOL_MAX` | 2 / 10 | asyncpg pool size per worker (compose: 8 / 12) |
| `DB_STATEMENT_TIMEOUT_MS` | 5000 | `SET LOCAL statement_timeout` for every transaction |
| `DB_STATEMENT_CACHE_SIZE` | 256 | asyncpg prepared statement cache; `0` behind PgBouncer < 1.21 |
| `DB_COMMAND_TIMEOUT_S` | 10 | client-side timeout |
| `SEARCH_MAX_CANDIDATES` | 1000 | per-type candidate cap for `/search` ranking |
| `DEFAULT_PAGE_SIZE` | 20 | default `limit` (max is always 100) |
| `LOG_LEVEL` | INFO | JSON logs to stdout |
| `WEB_CONCURRENCY` | 4 | uvicorn worker processes (image) |
| `SEED_BOOKS` / `SEED_AUTHORS` / `SEED_USERS` | 1000000 / 200000 / 50 | seed volume |
| `SEED_RANDOM` | 42 | seed for deterministic data |
| `SEED_WORKERS` / `SEED_CHUNK` | cpu-2 (≤8) / 20000 | generator processes / rows per COPY chunk |
| `SEED_TRUNCATE` | 1 | wipe catalogue tables before loading |

## API examples

```bash
API=http://localhost:18001/api/v1
# a user (whoever performs changes); writes require X-User-Id
U=$(curl -s -XPOST $API/users -H 'content-type: application/json' \
      -d '{"username":"anna","email":"anna@example.org"}' | jq -r .id)

A=$(curl -s -XPOST $API/authors -H "X-User-Id: $U" -H 'content-type: application/json' \
      -d '{"full_name":"Lev Tolstoy","original_name":"Лев Толстой","country_code":"RU","birth_date":"1828-09-09"}' | jq -r .id)

B=$(curl -s -XPOST $API/books -H "X-User-Id: $U" -H 'content-type: application/json' \
      -d "{\"title\":\"Война и мир\",\"isbn_13\":\"978-5-17-098746-7\",\"language_code\":\"ru\",
           \"publication_year\":1869,\"genres\":[\"novel\"],\"authors\":[{\"author_id\":\"$A\"}]}" | jq -r .id)

curl -s $API/books/$B -i | grep -i etag                       # ETag: "1"
curl -s -XPATCH $API/books/$B -H "X-User-Id: $U" -H 'If-Match: "1"' \
     -H 'content-type: application/json' -d '{"subtitle":"Том 1"}'     # version 2
curl -s -XPATCH $API/books/$B -H "X-User-Id: $U" -H 'If-Match: "1"' \
     -H 'content-type: application/json' -d '{"subtitle":"x"}'         # 409 problem+json
curl -s $API/books/$B/history | jq '.items[] | {action, changed_by_username, changed_at, diff}'

# lists: filters + whitelisted sort + keyset cursor (+ optional estimated total)
curl -s "$API/books?language=ru&year_from=1850&year_to=1900&sort=-publication_year&limit=20&include_total=true"
curl -s "$API/books?sort=title&limit=20&cursor=<next_cursor from previous page>"
curl -s "$API/books?q=war%20peace"            # full-text (websearch syntax)
curl -s "$API/books?title=karenin"            # substring/fuzzy (trigram)
curl -s "$API/books?isbn=0-14-044793-8"       # ISBN-10 or ISBN-13
curl -s "$API/books?author=tolst&genre=novel&publisher=Penguin%20Classics"
curl -s "$API/authors?country=RU&born_from=1800-01-01&sort=birth_date"
curl -s "$API/authors/$A/books?limit=50"
curl -s "$API/search?q=tolstoi&type=all"      # relevance (ts_rank + similarity), typo-tolerant
curl -s -XDELETE $API/books/$B -H "X-User-Id: $U"   # soft delete -> 204; GET -> 404; history keeps it
```

List response: `{"items": [...], "next_cursor": "…|null", "limit": 20, "total": 123|null, "total_is_estimate": true|false}`.
Errors follow RFC 7807 (`application/problem+json`): 400 (bad cursor/JSON/If-Match), 401 (no or unknown
X-User-Id), 404, 409 (version conflict, duplicate ISBN/username), 422 (validation, with `errors[]`),
428 (If-Match required), 503 (statement timeout).

## Search engine (when PostgreSQL FTS is no longer enough)

The required functionality is implemented with PostgreSQL alone: tsvector+GIN and pg_trgm. Add
OpenSearch/Elasticsearch when per-language stemming, synonyms or BM25 tuning are needed, when search
QPS competes with OLTP, or when the corpus is far past ~10⁸ documents per node. Do it as a read model:
Debezium CDC from logical replication (`wal_level=logical` is already on) or a transactional outbox →
consumer → index with external `version`. `/search` and `q` then go to OpenSearch with
`search_after` cursors, and cards are still read from PostgreSQL. Details are in ARCHITECTURE.md §5.

## Extrapolating to 10 billion books

Measured on the seeded DB (1M books, 200k authors, ~1.17M links, all rows also audited):

| relation | size @1M books | per book |
|---|---|---|
| books heap / indexes | 402 MB / 292 MB | ~0.40 KB / ~0.29 KB |
| book_authors + author_books | 267 MB | ~0.27 KB |
| book_isbns | 86 MB | ~0.09 KB |
| audit_log (1 row per entity) | 375 MB | ~0.31 KB |
| authors heap+idx (200k) | 111 MB | ~0.55 KB per author |
| **database total** | **1.44 GB** | **~1.4 KB** |

* **Disk:** 10B books × ~1.4 KB ≈ **14 TB** with the synthetic short descriptions. Real descriptions
  (≈1 KB avg, TOAST-compressed) and richer audit history push this to about **25–40 TB primary**,
  times replicas (×2–3) plus WAL/backup headroom. Authors (1B × 0.55 KB) add ~0.5 TB. GIN indexes
  (FTS + trigram) are about 45% of book index size. Moving FTS to OpenSearch removes them from the
  OLTP shards.
* **Layout:** 256 hash partitions, ~40M books per partition (~60 GB), spread over 16–32 shards
  (Citus or application routing) with 8–16 partitions each. The partition key equals the routing key
  for books, book_authors (book_id), authors and author_books (author_id).
* **Load time:** a single node does ~370k rows/s of COPY, which is ~65k books/s including links,
  ISBNs and audit (seed: 5.75M rows in 15.5 s). 10B books on one node would take ~43 h of COPY plus
  index builds. With 32 shards loading in parallel it is ~1.5 h of COPY. Index builds take ~24 s per
  1M books per node on 8 parallel builders and grow as n·log n, so ≈ 2–3 h per shard of 300M books.
  Build indexes per partition in parallel and attach them (`CREATE INDEX ON ONLY parent` + per-partition
  `CREATE INDEX CONCURRENTLY` + `ALTER INDEX … ATTACH PARTITION`).
* **Memory:** keep at least the upper B-tree levels and hot GIN pages in RAM. Per shard of 300M books,
  ~90 GB of indexes are needed, so plan for 128–256 GB RAM per shard.

## Layout

```
src/library/            api/ services/ repositories/ domain/ seed/ db.py config.py main.py
migrations/             alembic env + versions/0001_initial.py -> sql/0001_initial.{up,down}.sql
tests/unit              cursor, sorting, query building, validation, ISBN, seed generator, auth
tests/integration       real PostgreSQL via testcontainers (tmpfs PGDATA): CRUD, audit, keyset, filters, search, errors
tests/perf              EXPLAIN (ANALYZE, BUFFERS) on the seeded DB, fails on Seq Scan over books/authors
bench/k6.js             mixed read/write load scenario
deploy/postgres         tuned postgresql.conf; deploy/compose.devbox.yml (tmpfs PGDATA override)
```
