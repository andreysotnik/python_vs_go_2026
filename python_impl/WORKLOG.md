# Work log

## 1. Design and skeleton
* Read PROMPT.md and designed the schema: snowflake ids, hash partitions, a dual link table, an ISBN
  registry, monthly audit partitions, and partial indexes. Documented in docs/ARCHITECTURE.md.
* `uv` failed to reach PyPI through the configured HTTPS proxy (connection reset). A direct connection
  worked, so all uv calls run with `HTTPS_PROXY` unset (Makefile too).
* Migration 0001 (raw SQL via Alembic) applied on the first try.

## 2. Application
* domain (ISBN, cursor, sorting, models), repositories (QueryBuilder, SQL), services, API, problem+json,
  auth middleware, JSON logs, Prometheus middleware.
* Error: `asyncpg.Connection[Record]` is not subscriptable at runtime → aliases under TYPE_CHECKING.
* Smoke test against the local DB passed (curl quoting issues on my side only).

## 3. Seed
* Process pool generators (deterministic per chunk) feeding parallel binary COPY.
* **Docker VM disk 100% full** (other projects) → the first seed failed with `DiskFullError`. Switched
  PGDATA to a bind mount (later to tmpfs, see 6).
* Bug: `pg_get_indexdef()` of partitioned indexes returns `ON ONLY`, so the rebuilt "indexes" were empty
  parent stubs (built in 0.0 s). Strip `ON ONLY`. Caught by checking `pg_indexes` on a partition.
* 1M books / 200k authors: COPY 5.75M rows in 15–22 s (250–370k rows/s); total with indexes, FKs and
  VACUUM ANALYZE: 52–84 s depending on storage. The official `make seed` run on tmpfs: 52.0 s.

## 4. Tests: iterations to green
* Unit: first run had 3 failures. Two were my own wrong expected ISBN check digits; one long-tail
  threshold was too strict for 300 authors. Fixed in 1 iteration.
* Integration (testcontainers): 4 environment iterations. Ryuk could not mount the macOS socket path
  (socket override), then Ryuk port mapping failed (Ryuk disabled), then `with_kwargs(tmpfs=...)`
  clashed and the tmpfs size needs `size=1g` syntax. 1 data fix (invalid ISBN in the test fixture).
  Then 69/69 green.
* Lint: ruff (methods named `list` shadowed the builtin for mypy → renamed `find`), PLR0917 disabled for
  FastAPI handlers; mypy --strict green after 2 iterations.
* Total: about **7 fix iterations** until unit, integration, lint and mypy were all green.

## 5. EXPLAIN checks
* tests/perf builds the real repository SQL for 40 key queries, runs EXPLAIN (ANALYZE, BUFFERS) on the
  seeded DB, fails on Seq Scan over books/authors or on OFFSET, and writes docs/QUERY_PLANS.md.
  40/40 passed on the first run (after the index fix in 3).

## 6. Load test and performance fixes (4 iterations)
1. First k6 run: 31 rps, p50 1.4 s, 2% errors (timeouts). Postgres shared_buffers=4 GB on an 8 GB VM →
   swapping. Reduced to 1 GB.
2. Still ~40 rps. Found generic-plan problems (`LIMIT $n`, FTS selectivity) and relcache warm-up per new
   connection (0.5 s) → inlined LIMIT, connection warm-up, `plan_cache_mode=force_custom_plan`.
3. Still ~10 rps, with Postgres logs showing `bind` (planning) at ~600 ms under load and no wait events
   → diagnosed virtiofs bind-mount syscall latency. Moved PGDATA to tmpfs (devbox override).
4. Result: **560 rps, p50 95 ms, p95 266 ms, p99 412 ms, 0 errors** (64 VUs, 60 s, mixed workload).
   Tried 8 workers: worse (483 rps), reverted to 4.

## 7. Docker / compose
* The dockerfile frontend pull (`# syntax=`) timed out → removed the directive (built-in BuildKit is
  enough).
* `/tmp/prometheus` mkdir failed (VM disk full) → app container is read-only with tmpfs `/tmp`.
* Compose kept the old bind mount on `--force-recreate` → `docker compose rm -sf` and then up.

## 8. Final verification
* `make test`: 139 passed (70 unit + 69 integration), coverage 97.0% (gate 80%).
* `make lint`: ruff check/format clean, `mypy --strict` clean (41 files).
* `make explain`: 40/40 queries, no Seq Scan on books/authors, no OFFSET → docs/QUERY_PLANS.md.
* `make seed` (1M/200k, devbox tmpfs): 52.0 s total, COPY 371k rows/s.
* `make bench`: 559.8 rps, p50 95 ms / p95 266 ms / p99 412 ms, 0% errors.
* Default `docker compose up --build` (named volume, no override) on an empty volume: migrate ✓, app
  healthy, /docs 200; small `make seed SEED_BOOKS=20000` ✓ (the full 1M seed doesn't fit the full VM disk).
* Ended with `docker compose down -v`: only this project's volume was removed, to give the shared,
  full VM disk some room back.
