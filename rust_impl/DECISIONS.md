# Decisions and assumptions

Format: question → decision → why.

1. **Which Rust stack?** → axum 0.8 + tokio + sqlx 0.8 (runtime queries via `QueryBuilder`, no
   compile-time `query!` macros) + serde; sqlx embedded migrations (`sqlx::migrate!`).
   Why: the spec lists these; runtime-checked queries avoid needing a live DB (or `.sqlx` offline
   data) at compile time inside `docker build`; dynamic filter combinations need a builder anyway.

2. **Id format?** → 64-bit snowflake-like ids generated in the app (41 bit ms / 10 bit node /
   12 bit seq, epoch 2020-01-01), `NODE_ID` per replica; serialized as JSON strings.
   Why: monotonic (B-tree friendly), no central sequence (sharding), time-ordered; strings because
   JS numbers lose precision above 2^53. Input accepts strings or numbers.

3. **Global ISBN-13 uniqueness on a table partitioned by id?** → separate `isbn_registry`
   (PK isbn_13, HASH-partitioned by ISBN) written in the same transaction; also used for the
   `isbn` filter. ISBN-10 is converted to ISBN-13; soft delete releases the ISBN.
   Why: PostgreSQL requires the partition key in unique indexes on partitioned tables.

4. **Two link directions?** → two tables: `book_authors` (HASH book_id) and `author_books`
   (HASH author_id), maintained by the service in one transaction (not by a trigger).
   Why: each direction is a single-partition/single-shard lookup; a trigger would halve COPY
   throughput. Risk (divergence) is covered by a consistency test; reconciliation job would be
   needed in prod.

5. **Partition counts?** → books / links 64, authors 32, isbn_registry 16, audit_log monthly
   RANGE. Why: enough to show the design and keep the demo fast (planning cost grows with the
   partition count); production at 10B: 256 (documented).

6. **"B-tree for exact fields (language, publisher, genre)" vs index zoo?** → one multi-column
   GIN (btree_gin) on `(language_code, publisher, genres, created_by)` + a targeted B-tree
   `(language_code, title, id)` that the plan check proved necessary. `genres` is `text[]`
   (GIN, `@>`), ISBN goes through the registry. Why: any combination of equality filters from one
   index; fewer indexes to maintain on writes.

7. **NULL sort keys (publication_year, birth_date) with keyset pagination?** → expression
   indexes on `coalesce(publication_year, 32767)` / `coalesce(birth_date, 'infinity')`; the same
   expressions are used for ordering, keyset predicates and range filters (+ `IS NOT NULL` when a
   range filter is present). Why: row-comparison keyset predicates stay index conditions; NULLs
   sort last ascending / first descending like PostgreSQL does by default.

8. **Default sort?** → `-created_at` for books and authors; history ascending (oldest first);
   books of an author by book id (≈ creation order). Search: rank desc.

9. **Optimistic locking protocol?** → version in `If-Match` (`"3"`, `W/"3"` or `3`) or `version`
   in the body; responses carry `ETag: "<version>"`. Mismatch → **409** (as the task says; HTTP
   purists would use 412), missing on PUT/PATCH → **428**. DELETE: If-Match optional.

10. **400 vs 422?** → 400 for malformed requests (query params, cursor, path id, JSON syntax,
    unknown query params), 422 for well-formed JSON failing validation (incl. unknown body fields,
    wrong types, unknown author ids). 401 for missing/malformed/unknown `X-User-Id` on writes.

11. **Who is a user / bootstrap?** → `users` table with a bootstrap user id `1` ("system") created
    by the migration; `POST /api/v1/users` registers more (X-User-Id optional there). Unknown
    X-User-Id on writes → 401 (detected via the FK on created_by/updated_by).

12. **PATCH semantics?** → JSON merge patch: absent = unchanged, `null` = clear (rejected for
    non-nullable fields); `authors` in PATCH/PUT replaces the whole list. Changing one ISBN form
    re-derives the other.

13. **statement_timeout with PgBouncer?** → `set_config('statement_timeout', …, is_local=true)` at
    the start of every transaction (one extra round trip, no session state).

14. **Custom vs generic plans?** → keep `plan_cache_mode=auto` (generic plans after 5 runs) and
    make the queries generic-plan safe (LATERAL primary-key lookups) + verify generic plans in the
    plan checker (`EXPLAIN (GENERIC_PLAN)`, PG16+). Tried `force_custom_plan` first: ~4 ms planning
    per query over 64 partitions, throughput ÷3.

15. **Search relevance cost?** → two modes: full text (`@@`), re-ranking only the best 1000 by
    `ts_rank` with `+ similarity()`; fuzzy (`<%`, word_similarity) only when full text finds
    nothing; mode is kept in the cursor. Consequence: at most 1000 hits per entity type.
    Why: similarity for every match of a frequent term was 80% of query time.

16. **Search "rank + id" keyset across two tables?** → keyset over `(rank, type, id)` (type breaks
    ties because ids are only unique per table in principle); cursor `[rank, type, mode]` + id;
    `serde_json` with `float_roundtrip` keeps f64 ranks exact.

17. **`title`/`name`/`author` filters shorter than 3 chars?** → 400. Why: trigram indexes cannot
    serve shorter patterns, the query would degrade to a scan.

18. **Unknown query parameters?** → 400 with the list of allowed ones (catches `offset=`, typos).

19. **Swagger UI assets?** → `/docs` serves a small HTML page loading `swagger-ui-dist@5.17.14`
    from unpkg; the spec is embedded in the binary (`/openapi.yaml`). Why: no extra crate that
    downloads assets at build time; the browser needs internet access for the UI assets (the API
    and the spec do not). Spec validated with `redocly lint` (valid; 5 style warnings) and by an
    integration test that every documented operation is routed.

20. **How is `make test` runnable "on a clean machine"?** → `make test` runs `cargo test` inside
    the official `rust:1.100` image (compose profile `test`) against the compose PostgreSQL; each
    run creates a fresh database (`wl_test_*`) and applies the real migrations. testcontainers
    were not needed. Local alternative: `make test-local`.

21. **Seed determinism vs "now"?** → time range fixed to [2021-01-01, 2026-11-01) (capped at
    now−1 day), all values derived from `hash(SEED_RANDOM, table, row index)` → identical data and
    ids for any chunking/worker count (tested by re-seeding and comparing an md5 digest).
    Seed truncates catalog tables (`SEED_TRUNCATE=true`) and keeps the bootstrap user.
    Seed writes one `create` audit entry per author/book so history is non-empty.

22. **Index rebuild after COPY on partitioned tables?** → `CREATE INDEX … ON ONLY parent`, build
    each partition's index in parallel (8 connections), `ALTER INDEX … ATTACH PARTITION`; FKs
    re-added after; DDL kept in `seed_pending_ddl` for crash recovery.

23. **Network / proxy.** The host HTTPS proxy resets connections; cargo was run with proxy vars
    unset (crates.io reachable). Docker builds have direct network. The `# syntax=docker/dockerfile:1`
    directive failed (TLS timeout to auth.docker.io) → removed (not needed).

24. **Runtime image?** → `gcr.io/distroless/cc-debian12:nonroot` (34.6 MB image, no shell,
    uid nonroot). The binary has a `healthcheck` sub-command for the Docker HEALTHCHECK.

25. **Linters.** → `cargo fmt --check` + `cargo clippy --all-targets -- -D warnings`
    (the Rust counterpart of golangci-lint / ruff+mypy).

26. **Coverage target "≥ 80% for business logic".** → measured with `cargo llvm-cov` over unit +
    integration tests: 93.9% lines overall; service/query/validation/repository 95–100%.
    `main.rs` / `lib.rs::serve` (process wiring) are exercised by the Docker run, not by tests.

27. **Load testing tool.** → `grafana/k6` Docker image on the compose network (k6 is not installed
    locally); report written by `handleSummary` to `bench/results/k6-report.md`.
