"""`python -m library.seed` - bulk-load mock data with COPY.

Steps: (optionally) truncate -> drop secondary indexes and FKs -> parallel generate+COPY ->
recreate FKs and indexes in parallel -> VACUUM ANALYZE. Progress and rows/s are logged as JSON.
"""

from __future__ import annotations

import asyncio
import logging
import os
import time
from concurrent.futures import ProcessPoolExecutor
from typing import Any

import asyncpg

from library.db import Connection
from library.logging_setup import configure_logging
from library.seed.generator import (
    AUDIT_COLUMNS,
    AUTHOR_BOOK_COLUMNS,
    AUTHOR_COLUMNS,
    BOOK_COLUMNS,
    ISBN_COLUMNS,
    LINK_COLUMNS,
    SeedParams,
    generate_authors,
    generate_books,
    generate_users,
)

log = logging.getLogger("library.seed")

COLUMNS = {
    "authors": AUTHOR_COLUMNS,
    "books": BOOK_COLUMNS,
    "book_isbns": ISBN_COLUMNS,
    "book_authors": LINK_COLUMNS,
    "author_books": AUTHOR_BOOK_COLUMNS,
    "audit_log": AUDIT_COLUMNS,
}
BULK_TABLES = ("books", "authors", "book_authors", "author_books", "book_isbns", "audit_log")


def _env_int(name: str, default: int) -> int:
    return int(os.environ.get(name, default))


async def _drop_secondary(conn: Connection) -> tuple[list[str], list[str]]:
    """Capture DDL of secondary indexes and FKs (from the catalog, so migrations stay the source of truth)."""
    idx = await conn.fetch(
        "SELECT c.relname AS name, pg_get_indexdef(i.indexrelid) AS ddl FROM pg_index i "
        "JOIN pg_class c ON c.oid = i.indexrelid "
        "WHERE i.indrelid = ANY($1::regclass[]) AND NOT i.indisprimary AND NOT i.indisunique",
        list(BULK_TABLES),
    )
    fks = await conn.fetch(
        "SELECT conname, conrelid::regclass::text AS tbl, pg_get_constraintdef(oid) AS def FROM pg_constraint "
        "WHERE contype = 'f' AND conparentid = 0 AND conrelid = ANY($1::regclass[])",
        list(BULK_TABLES),
    )
    for fk in fks:
        await conn.execute(f'ALTER TABLE {fk["tbl"]} DROP CONSTRAINT "{fk["conname"]}"')
    for ix in idx:
        await conn.execute(f'DROP INDEX "{ix["name"]}"')
    log.info("dropped secondary indexes and FKs", extra={"indexes": len(idx), "fks": len(fks)})
    # pg_get_indexdef() of a partitioned index says "ON ONLY parent" (parent stub only);
    # without ONLY the index is built on every partition and attached.
    index_ddl = [ix["ddl"].replace(" ON ONLY ", " ON ", 1) for ix in idx]
    fk_ddl = [f'ALTER TABLE {fk["tbl"]} ADD CONSTRAINT "{fk["conname"]}" {fk["def"]}' for fk in fks]
    return index_ddl, fk_ddl


class Progress:
    def __init__(self, entity: str, total: int) -> None:
        self.entity, self.total, self.done = entity, total, 0
        self.rows = 0
        self.start = time.perf_counter()
        self.last_log = 0.0

    def add(self, entities: int, rows: int) -> None:
        self.done += entities
        self.rows += rows
        now = time.perf_counter()
        if now - self.last_log > 2 or self.done >= self.total:
            self.last_log = now
            elapsed = now - self.start
            log.info(
                "progress",
                extra={
                    "entity": self.entity,
                    "done": self.done,
                    "total": self.total,
                    "pct": round(100 * self.done / max(self.total, 1), 1),
                    "rows_written": self.rows,
                    "rows_per_s": int(self.rows / elapsed) if elapsed else None,
                    "entities_per_s": int(self.done / elapsed) if elapsed else None,
                },
            )


async def _load(
    pool: asyncpg.Pool,
    executor: ProcessPoolExecutor,
    fn: Any,
    params: SeedParams,
    total: int,
    chunk_size: int,
    entity: str,
    copy_slots: int,
) -> int:
    loop = asyncio.get_running_loop()
    progress = Progress(entity, total)
    sem = asyncio.Semaphore(copy_slots * 2)  # bounds memory: generated-but-not-copied chunks

    async def one(chunk: int, start: int, end: int) -> None:
        async with sem:
            tables = await loop.run_in_executor(executor, fn, params, chunk, start, end)
            async with pool.acquire() as conn:
                rows = 0
                for table, records in tables.items():
                    if records:
                        await conn.copy_records_to_table(table, records=records, columns=COLUMNS[table])
                        rows += len(records)
            progress.add(end - start, rows)

    ranges = [(c, s, min(s + chunk_size, total)) for c, s in enumerate(range(0, total, chunk_size))]
    await asyncio.gather(*(one(*r) for r in ranges))
    return progress.rows


async def _run_parallel(pool: asyncpg.Pool, statements: list[str], what: str) -> None:
    async def one(sql: str) -> None:
        t0 = time.perf_counter()
        async with pool.acquire() as conn:
            await conn.execute("SET maintenance_work_mem = '256MB'; SET max_parallel_maintenance_workers = 2")
            await conn.execute(sql)
        log.info(what, extra={"sql": sql[:160], "seconds": round(time.perf_counter() - t0, 1)})

    await asyncio.gather(*(one(s) for s in statements))


async def main() -> None:
    configure_logging(os.environ.get("LOG_LEVEL", "INFO"))
    dsn = os.environ.get("DATABASE_URL", "postgresql://library:library@localhost:15432/library")
    n_books = _env_int("SEED_BOOKS", 1_000_000)
    n_authors = _env_int("SEED_AUTHORS", 200_000)
    n_users = _env_int("SEED_USERS", 50)
    seed = _env_int("SEED_RANDOM", 42)
    workers = _env_int("SEED_WORKERS", max(2, min(8, (os.cpu_count() or 4) - 2)))
    chunk = _env_int("SEED_CHUNK", 20_000)
    truncate = os.environ.get("SEED_TRUNCATE", "1") not in ("0", "false", "no")
    log.info("seed start", extra={"books": n_books, "authors": n_authors, "seed": seed, "workers": workers})

    t_start = time.perf_counter()
    pool = await asyncpg.create_pool(dsn, min_size=workers, max_size=workers + 2, command_timeout=None)
    assert pool is not None
    async with pool.acquire() as conn:
        existing = await conn.fetchval("SELECT EXISTS (SELECT 1 FROM books)")
        if existing and not truncate:
            raise SystemExit("database already contains books; set SEED_TRUNCATE=1 to reload")
        await conn.execute(
            "TRUNCATE audit_log, author_books, book_authors, book_isbns, books, authors; "
            "DELETE FROM users WHERE id <> 1"
        )
        index_ddl, fk_ddl = await _drop_secondary(conn)
        users = generate_users(seed, n_users)
        await conn.copy_records_to_table("users", records=users, columns=("id", "username", "email", "created_at"))
    params = SeedParams(seed=seed, n_books=n_books, n_authors=n_authors, user_ids=tuple(sorted(u[0] for u in users)))

    t_load = time.perf_counter()
    with ProcessPoolExecutor(max_workers=workers) as executor:
        rows_a = await _load(pool, executor, generate_authors, params, n_authors, chunk, "authors", workers)
        rows_b = await _load(pool, executor, generate_books, params, n_books, chunk, "books", workers)
    load_s = time.perf_counter() - t_load
    total_rows = rows_a + rows_b + n_users
    log.info(
        "copy done", extra={"rows": total_rows, "seconds": round(load_s, 1), "rows_per_s": int(total_rows / load_s)}
    )

    t_idx = time.perf_counter()
    # Largest builds first so the parallel slots are used evenly.
    index_ddl.sort(key=lambda d: (" gin " not in d.lower(), "books" not in d))
    await _run_parallel(pool, index_ddl, "index built")
    await _run_parallel(pool, fk_ddl, "fk validated")
    idx_s = time.perf_counter() - t_idx

    t_an = time.perf_counter()
    await _run_parallel(pool, [f"VACUUM (ANALYZE) {t}" for t in (*BULK_TABLES, "users")], "vacuum analyze")
    analyze_s = time.perf_counter() - t_an
    await pool.close()

    total_s = time.perf_counter() - t_start
    log.info(
        "seed finished",
        extra={
            "books": n_books,
            "authors": n_authors,
            "rows_total": total_rows,
            "copy_seconds": round(load_s, 1),
            "copy_rows_per_s": int(total_rows / load_s),
            "index_fk_seconds": round(idx_s, 1),
            "vacuum_analyze_seconds": round(analyze_s, 1),
            "total_seconds": round(total_s, 1),
        },
    )


if __name__ == "__main__":
    asyncio.run(main())
