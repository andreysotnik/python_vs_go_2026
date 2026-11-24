"""asyncpg pool + transaction helper (PgBouncer transaction-mode friendly)."""

from __future__ import annotations

from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from typing import TYPE_CHECKING, Any, TypeAlias

import asyncpg
import orjson

from library.config import Settings

if TYPE_CHECKING:
    from asyncpg.pool import PoolConnectionProxy

    Connection: TypeAlias = asyncpg.Connection[asyncpg.Record] | PoolConnectionProxy[asyncpg.Record]  # noqa: UP040
    Pool: TypeAlias = asyncpg.Pool[asyncpg.Record]  # noqa: UP040
else:
    Connection = asyncpg.Connection
    Pool = asyncpg.Pool


# Planning against 64-partition tables opens every partition and index once per backend
# (relcache). Doing it when the connection is created keeps that ~0.5 s off user requests.
WARMUP_SQL = tuple(
    f"EXPLAIN SELECT 1 FROM {t} WHERE {c} > 0 ORDER BY {c}"
    for t, c in (
        ("books", "id"),
        ("authors", "id"),
        ("book_authors", "book_id"),
        ("author_books", "author_id"),
        ("book_isbns", "book_id"),
        ("audit_log", "entity_id"),
    )
)


async def _init_connection(conn: Connection) -> None:
    for sql in WARMUP_SQL:
        await conn.execute(sql)
    for typ in ("jsonb", "json"):
        await conn.set_type_codec(
            typ,
            encoder=lambda v: orjson.dumps(v).decode(),
            decoder=orjson.loads,
            schema="pg_catalog",
        )


async def create_pool(settings: Settings) -> Pool:
    return await asyncpg.create_pool(
        dsn=settings.database_url,
        min_size=settings.db_pool_min,
        max_size=settings.db_pool_max,
        statement_cache_size=settings.db_statement_cache_size,
        command_timeout=settings.db_command_timeout_s,
        max_inactive_connection_lifetime=300,
        init=_init_connection,
        server_settings={"application_name": "world-library"},
    )


class Database:
    """Every unit of work runs in an explicit transaction with SET LOCAL timeouts.

    No session-level state is used (no session SET, no advisory locks, no LISTEN), so the
    pool can sit behind PgBouncer in transaction mode.
    """

    def __init__(self, pool: Pool, statement_timeout_ms: int) -> None:
        self.pool = pool
        self._timeout = int(statement_timeout_ms)

    @asynccontextmanager
    async def tx(self, *, readonly: bool = False) -> AsyncIterator[Connection]:
        mode = "READ ONLY" if readonly else "READ WRITE"
        async with self.pool.acquire() as conn:
            # One round trip: BEGIN + timeouts (simple query protocol allows several statements).
            await conn.execute(
                f"BEGIN ISOLATION LEVEL READ COMMITTED {mode}; "
                f"SET LOCAL statement_timeout = {self._timeout}; "
                f"SET LOCAL lock_timeout = {min(self._timeout, 2000)}; "
                f"SET LOCAL idle_in_transaction_session_timeout = {self._timeout * 2}; "
                # Always plan with the actual parameter values: generic plans can't prune hash
                # partitions at plan time and guess FTS/trigram selectivity (a rare search term
                # would then walk a whole sort index). Planning cost is ~1 ms.
                "SET LOCAL plan_cache_mode = force_custom_plan"
            )
            try:
                yield conn
            except BaseException:
                await conn.execute("ROLLBACK")
                raise
            else:
                await conn.execute("COMMIT")

    async def ping(self) -> Any:
        async with self.pool.acquire() as conn:
            return await conn.fetchval("SELECT 1")
