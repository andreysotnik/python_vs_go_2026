# Decisions & assumptions (Go implementation)

Format: question → decision → why.

1. **Dependencies offline?** Corporate GOPROXY and proxy.golang.org were unreachable (TLS timeouts).
   → Resolved modules from the local module cache (`GOPROXY=off`/`file://…/cache/download`), committed
   `vendor/`, Docker build uses `-mod=vendor`. → Reproducible builds with no network; pgx 5.11 forced
   `go 1.26` in go.mod, hence the `golang:1.26-alpine` build image.
2. **Router / SQL builder?** chi v5 + pgx v5 (pool) + squirrel (dynamic WHERE) → filters are optional
   and combinatorial, which suits a builder better than sqlc's static queries; repository keeps SQL
   explicit.
3. **Id type?** 63-bit snowflake `bigint`, JSON string. → 8 bytes, time-ordered (B-tree friendly),
   no UUIDv4. Strings because ids exceed 2^53 (JS).
4. **Partition counts?** books / book_authors / author_books: 64 (HASH), authors / book_isbns: 32.
   → Lower bound of the requested 64–256 for the demo; production would use 256–1024.
5. **Global ISBN uniqueness on a hash-partitioned table?** → separate `book_isbns` table partitioned by
   ISBN with PK, maintained transactionally. ISBN-10 is converted to ISBN-13 (single lookup path).
6. **Both directions of book↔author?** → `book_authors` (by book) + mirror `author_books` (by author)
   written in the same transaction (application-level, not a trigger: explicit, shard-portable, cheaper
   for COPY).
7. **Nullable sort fields in keyset?** → NULLs always last, two-phase query (non-null range, then
   NULL tail), cursor value `null` = "in the NULL tail". Both phases use the same `(col, id)` index.
8. **400 vs 422?** → 400: malformed query params/cursor/JSON syntax or types/unknown fields/bad
   Content-Type; 422: well-formed body violating business rules (check digits, ranges, unknown author).
9. **PUT/PATCH without version?** → 428 Precondition Required; `If-Match` wins over body `version`;
   DELETE accepts optional `If-Match`. Stale version → 409 (as required).
10. **Authentication?** → `X-User-Id` must reference an existing user (401 otherwise), behind an
    `Authenticator` interface. Added `POST/GET /api/v1/users` (not in the spec) so users can be created.
11. **What does `updated_by` hold on creation?** → equals `created_by` (columns are NOT NULL; "who last
    changed it" is always answerable).
12. **Soft delete semantics?** → deleted rows return 404 on card/update, disappear from lists (partial
    indexes), history stays available; a deleted book releases its ISBN; a deleted author keeps link
    rows but disappears from book cards.
13. **History of seeded data?** → seed writes a `create` audit row per author/book (1.2M rows), so
    history is non-empty for every entity.
14. **Sort for `/authors/{id}/books`?** → by `(book_id, role)` (PK of `author_books`, single partition).
    Book id ≈ creation time. Other orders would need denormalized columns in the mirror.
15. **`author=` filter (name of author)?** → resolved through the trigram index on authors, capped at
    1000 matching authors (documented) to keep the semi-join bounded.
16. **Minimum length for substring filters?** → 3 characters (one full trigram), otherwise 400, because
    shorter patterns cannot use the trigram index.
17. **Birth dates BC?** → `birth_date/death_date` restricted to years 0001..2100 (JSON `YYYY-MM-DD`
    cannot express BC dates cleanly); `publication_year` allows -3000.
18. **statement_timeout with PgBouncer?** → direct mode: startup parameter (no extra round trip);
    `DB_PGBOUNCER=true`: no session state, `QueryExecModeExec`, timeout set on the role.
19. **Test infrastructure?** → compose `test` profile (no testcontainers: avoids pulling ryuk with a
    flaky registry connection). Tests create/drop their own databases on the compose PostgreSQL. The
    test container mounts sources read-only and keeps the Go build cache in tmpfs (shared Docker disk
    was full, see 22).
20. **Coverage scope ("business logic")?** → `config, cursor, domain, httpapi, idgen, isbn, repository,
    service`. Excluded: tooling (`seed` loader, `explain`, `dbx`, `migrate`, `cmd`). The seed loader and
    explain tool are still exercised by an integration test.
21. **Language filter + title sort was slow (222 ms)** on the seeded DB because Cyrillic/CJK titles sort
    far from Latin ones (collation-correlated filter). → migration 00004 adds
    `(language_code, title, id)`; now < 1 ms. Other facet×sort combinations are intentionally not
    indexed (write amplification) — they never Seq Scan but can be slower for rare values.
22. **Shared Docker disk ran full** (59 GB VM disk, used by other projects/the parallel agent).
    PostgreSQL crashed with "No space left on device" during the 2nd seed and could not recover.
    → Removed only my own resources (`lib_go_pgdata` volume, my dangling `lib_go-app` images, the lint
    and Go 1.25 images I had pulled), reduced `max_wal_size` to 512MB with `wal_recycle=off`, and
    re-seeded. Production values are noted in comments (16–64 GB WAL).
23. **Parallel query?** → `max_parallel_workers_per_gather = 0`: short LIMIT queries do not benefit and
    parallel workers multiplied CPU contention under load on a shared 12-CPU VM.
24. **Generic plans (`plan_cache_mode`)?** Tried `force_generic_plan` to amortize the ~5 ms planning of
    64-partition queries; gain was within noise on the overloaded host and it would make runtime plans
    differ from the EXPLAIN-verified ones → reverted to `auto`, documented as a tuning option.
25. **golangci-lint version?** Local binary (built with go1.25) refuses go1.26 modules; rebuilt with
    go1.27 it cannot read go1.27 export data. → `make lint` uses the `golangci/golangci-lint:v2.12.2`
    image (Go 1.26).
26. **Swagger UI assets?** → served from unpkg CDN (`swagger-ui-dist@5.17.14`) by a tiny HTML page at
    `/docs`; the spec itself is embedded in the binary.
27. **Book card/list authors: JOIN or two queries?** → two queries (links by `book_id = ANY`, authors by
    `id = ANY`) joined in Go: with a JOIN the planner chose a Seq Scan over all `authors` partitions for
    20+ ids (found under load, not by the 1-id EXPLAIN case; the checker now uses a real 100-row page).
28. **EXPLAIN coverage of generic plans?** → the checker verifies custom plans (parameters known), which
    is what PostgreSQL uses for the first executions and under `plan_cache_mode=auto` when cheaper.
    Generic plans are not verified separately (known gap).
29. **Lint verification without network/disk?** → `make lint-offline` builds golangci-lint v2.12.2 from the
    host Go module cache inside a tmpfs container (the cache was already used for dependencies).
