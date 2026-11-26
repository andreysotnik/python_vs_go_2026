# Work log

## 1. Design (≈ 20 min)
* Read PROMPT.md, chose axum + sqlx + tokio, snowflake ids, hash partitioning (books/links 64,
  authors 32), mirrored link tables, ISBN registry, monthly audit partitions, keyset cursors,
  btree_gin multi-column index for equality filters. Wrote the migration first.

## 2. Implementation
* Modules: config, ids, error (RFC 7807), cursor, isbn, models, validation, query (filter / sort /
  keyset SQL builders), repository, service, http (router, extractors, auth + observe middleware),
  metrics, seed (data dictionaries + parallel COPY loader), explain (plan checker), main (CLI).
* First `cargo build`: 1 type error (BookRow::data signature) + 1 deprecation (TimeoutLayer::new) — fixed.
* Unit tests: 57 passed on the first run.

## 3. Integration tests (green on the 5th run)
1. 8 failures: (a) concurrent test apps all used snowflake node 7 → duplicate ids in the same
   millisecond (409 on PK) — each test app now gets its own node id (exactly what replicas need);
   (b) wrong expectation in a test (`sort=title` is invalid for authors); (c) openapi placeholder.
2. Test requests with spaces / Cyrillic in the URI failed to build → percent-encoding helper.
3. Wrong expectation: `q=-poetry` — genres are not part of the search vector.
4. `created_from=now` truncated to seconds included just-created rows → use now + 1 min.
5. All 14 green. Clippy clean, rustfmt applied (`rustfmt.toml`: max_width 140).

## 4. Docker
* `# syntax=docker/dockerfile:1` → TLS handshake timeout to auth.docker.io → directive removed.
* Image built, but the app exited silently with code 0: the dependency-caching layer's dummy
  `main` survived because COPY keeps old mtimes and cargo's fingerprint looked fresh → remove
  fingerprints of the crate + `touch src/*.rs` before the real build. Image: distroless nonroot, 34.6 MB.

## 5. Seed (1M books / 200k authors)
* First run worked: 33.9 s total, COPY 732k rows/s, 682 partition indexes rebuilt in parallel.
* Later: made data fully deterministic (the time range depended on `now`) — caught by a new
  end-to-end test that re-seeds and compares an md5 digest.

## 6. EXPLAIN check
* All 27 queries without Seq Scan on the first run, but `language=ja&sort=title` took 762 ms:
  CJK titles sort after Latin ones, the title index scan filtered 870k rows → migration 2 adds
  `(language_code, title, id)` → 2 ms.

## 7. Load test and the generic-plan problem (most valuable iteration)
* k6 run 1: 215 req/s, p50 ~110 ms, PostgreSQL at 1160% CPU. I had forced
  `plan_cache_mode=force_custom_plan` → 3–5 ms planning per query (64 partitions). Removed it: no gain.
* pg_stat_statements: the page-enrichment query `book_authors JOIN authors WHERE book_id = ANY($1)`
  averaged 84 ms: its **generic** plan hashed the whole authors table (parallel seq scan), while
  the EXPLAIN check had only seen the fine custom plan. Rewrote as `unnest($1)` + LATERAL PK
  lookups → 0.5 ms.
* Extended the plan checker with `EXPLAIN (GENERIC_PLAN)` for every query → it immediately found
  a generic Seq Scan in the `author` (name) filter. LATERAL rewrite was flattened back by the
  planner → added `LIMIT 1` fence (id is PK) → pass.
* Search was 85–90% of DB time (frequent seed words match 25k+ rows; `similarity()` per row = 80%
  of the cost). Split into full-text mode (re-rank best 1000 by ts_rank) and fuzzy fallback (only
  when full text finds nothing). A first version used an `EXISTS` probe to choose the mode — its
  generic plan was a Seq Scan → replaced by "run full text, fall back if empty".
* Result: 215 → 658 req/s, p95 418 → 160 ms overall, search p50 398 → 158 ms.

## 8. Coverage, spec, final run
* Added `tests/seed_explain.rs` (seed 3k books into an exclusive DB, verify counts/links/ISBNs/
  restored indexes/determinism, run the plan checker) → coverage 83% → 93.9% lines.
* `redocly lint` on openapi.yaml: only `security-defined` errors → X-User-Id became an apiKey
  security scheme (Swagger "Authorize" button); spec valid.
* Final clean cycle: `docker compose down -v` → `docker compose up --build` → `make seed` (33.0 s)
  → `make test` in a container (72 passed) → `make explain` (29 queries PASS) → `make bench`
  (60 s, 658 req/s, 0 errors) → `make lint` clean → `docker compose down`.

Iterations to green: unit 1, integration 5, docker 2, plan check 2 (+2 for generic plans), search perf 3.
