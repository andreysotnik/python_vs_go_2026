# Decisions and assumptions

Format: question → decision → why.

1. **Data access: SQLAlchemy or asyncpg?** → Raw asyncpg plus a tiny QueryBuilder; SQLAlchemy is used
   only by Alembic. → Full control over the SQL that must match the index expressions
   (`coalesce(...)`, `f_unaccent(...)`), binary COPY for the seed, and lowest overhead.
2. **Migrations as Python or SQL?** → Alembic revision files that execute versioned `.sql` files
   (`migrations/sql/0001_initial.{up,down}.sql`). → The DDL is reviewable as plain SQL and the tooling
   is still Alembic, as required.
3. **Id format** → 63-bit snowflake bigint from `next_id()` (node 1); the seed generates the same layout
   on nodes 2–6. JSON carries ids as strings. → Monotonic B-tree inserts, globally unique ids (needed
   for the `/search` `(score,id)` keyset across types), and no JS precision loss.
4. **Partition counts** → books 64, book_authors/author_books 64, authors 32, book_isbns 16, audit
   monthly. → Lower end of the "64–256" requirement. More partitions measurably increase planning cost
   on a single node (see ARCHITECTURE §2); 256 is recommended once sharded.
5. **Global ISBN-13 uniqueness on a partitioned table** → Registry table `book_isbns(isbn_13 PK)`
   partitioned by ISBN, maintained in the same transaction; a soft delete releases the ISBN.
   → A UNIQUE index must contain the partition key (id), so it cannot enforce global ISBN uniqueness.
6. **Book↔author relation in both directions** → Two tables (`book_authors` by book_id,
   `author_books` by author_id), written together. → Both lookups prune to one partition (one shard).
7. **NULL publication_year / birth_date in keyset sorts** → Index and sort on
   `coalesce(year, 32767)` / `coalesce(birth_date, 'infinity')`. NULLs come last ascending and first
   descending. `year_from` adds `IS NOT NULL`. → One row-value range condition, so keyset stays O(log n).
8. **Which endpoints need `version`?** → Books (required by the spec) **and authors**; users have none.
   PUT/PATCH need `If-Match: "<version>"` or `version` in the body: missing → **428**, stale → **409**.
   DELETE accepts an optional If-Match. → The same concurrency safety for both editable entities.
9. **PUT semantics** → Full replacement: omitted optional fields become null/empty, including `authors`.
   PATCH uses JSON merge-patch (absent = unchanged, `null` = clear). The merged document is re-validated
   (422).
10. **400 vs 422** → 400 for malformed transport input (cursor, JSON syntax, If-Match format,
    unsupported sort reaching the service); 422 for semantic validation of body or query (including
    `limit>100` and a sort outside the OpenAPI enum/pattern). → Matches the FastAPI/RFC 7807 convention.
11. **Users** → Minimal `POST /api/v1/users`, `GET /api/v1/users/{id}` (not in the spec). Writes require
    an existing user in `X-User-Id` (401 otherwise). A system user id=1 is created by the migration.
    → Somebody has to own `created_by`, and the tests need to create users.
12. **History for seeded rows** → The seed writes one synthetic `create` audit row per book and author
    (COPY), so every record has history. → Consistent "who/when created" through the API.
13. **Seed and FKs/indexes** → The seed reads the secondary index DDL and FK DDL from the catalog,
    drops them, COPYs, and rebuilds them in parallel. It strips `ON ONLY` from `pg_get_indexdef` of
    partitioned indexes (bug found: otherwise only empty parent stubs were built). → The migrations
    stay the single source of truth.
14. **Search candidate cap** → `/search` ranks at most `SEARCH_MAX_CANDIDATES` (1000) matches per type,
    newest first. → Bounded latency; a known limitation, documented as the trigger for OpenSearch.
15. **`title`/`name` filters** → Substring/fuzzy via trigram, minimum 3 characters (422 otherwise).
    The `author` filter is full-text with prefix on the last word. → A trigram index can't serve
    patterns under 3 characters, and an exact-token author match is far more selective than a substring.
16. **Generic vs custom plans** → `SET LOCAL plan_cache_mode = force_custom_plan` per transaction and
    `LIMIT` inlined as a literal. → Under load, generic plans for rare FTS terms walked an entire sort
    index (5 s queries), and `LIMIT $n` made the planner assume 10% of rows. Planning cost with custom
    plans is ~1 ms.
17. **Connection warm-up** → The pool `init` runs `EXPLAIN` over every partitioned table. → The first
    query on a new backend paid ~0.3–0.5 s loading the relcache for 64 partitions × 10 indexes.
18. **Environment: Docker VM disk 100% full (other projects' images/volumes) and the corporate HTTPS
    proxy broken** → uv runs with `HTTPS_PROXY` unset (in the Makefile too). PGDATA cannot use a named
    volume on this machine, so it lives on tmpfs through `deploy/compose.devbox.yml`
    (`COMPOSE_FILE=docker-compose.yml:deploy/compose.devbox.yml`). The default `docker-compose.yml`
    uses a named volume `lib_python_pgdata` (correct for a clean machine). Nothing belonging to other
    projects was removed.
19. **Why not a bind mount for PGDATA?** → It was tried first. On Docker Desktop/virtiofs it cut
    throughput by ~50× (10 rps, planning in "bind" taking 600 ms under load, no DB wait events), because
    planning over 64 partitions issues per-relation file syscalls through virtiofs. tmpfs gives 560 rps
    with the same code.
20. **Testcontainers on Docker Desktop** → `TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock`,
    Ryuk disabled (its port mapping failed; the fixture's context manager removes the container), and
    PGDATA on tmpfs (the VM disk is full). `TEST_DATABASE_URL` can point the tests at any existing DB
    instead.
21. **PostgreSQL tuning values** → Sized for the Docker VM (8 GB RAM shared with another Postgres):
    shared_buffers 1 GB (4 GB caused swapping), work_mem 16 MB, maintenance_work_mem 256 MB, JIT off,
    random_page_cost 1.1, wal_level=logical (CDC readiness), pg_stat_statements.
22. **uvicorn workers** → 4 workers × pool 8–12. 8 workers measured worse (483 rps vs 560 rps; the DB is
    the bottleneck on the shared VM). Both outputs are in `bench/results/`.
23. **Load tool** → k6 from the `grafana/k6:0.54.0` image as a compose service (profile `bench`) on the
    compose network. Scenario: 64 VUs for 60 s, a mixed read/write workload (~95% reads, 5% create+patch).
