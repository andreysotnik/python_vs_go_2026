"""Authors persistence."""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

import asyncpg

from library.db import Connection
from library.domain.cursor import Cursor
from library.domain.filters import AuthorFilters
from library.domain.sorting import Sort
from library.repositories.books import SELECT_COLS as BOOK_SELECT_COLS
from library.repositories.common import explain_rows
from library.repositories.sql import QueryBuilder, keyset_clause, like_escape, order_by

AUTHOR_COLUMNS = (
    "full_name",
    "original_name",
    "birth_date",
    "death_date",
    "country_code",
    "biography",
    "external_ids",
)
SELECT_COLS = (
    "a.id, "
    + ", ".join(f"a.{c}" for c in AUTHOR_COLUMNS)
    + ", a.created_at, a.created_by, a.updated_at, a.updated_by, a.version"
)
NAME_EXPR = "f_unaccent(a.full_name || ' ' || coalesce(a.original_name, ''))"


def build_filters(f: AuthorFilters) -> QueryBuilder:
    qb = QueryBuilder().where("a.deleted_at IS NULL")
    if f.q:
        qb.where("a.search_vector @@ websearch_to_tsquery('simple', f_unaccent({}))", f.q)
    if f.name:
        qb.where(f"{NAME_EXPR} ILIKE '%' || f_unaccent({{}}) || '%'", like_escape(f.name))
    if f.country:
        qb.where("a.country_code = {}", f.country)
    if f.born_from is not None:
        qb.where("coalesce(a.birth_date, 'infinity'::date) >= {}::date", f.born_from)
        qb.where("a.birth_date IS NOT NULL")
    if f.born_to is not None:
        qb.where("coalesce(a.birth_date, 'infinity'::date) <= {}::date", f.born_to)
    return qb


def build_list_query(f: AuthorFilters, sort: Sort, cursor: Cursor | None, limit: int) -> tuple[str, list[Any]]:
    qb = build_filters(f)
    keyset_clause(qb, sort, cursor, "a.id")
    # LIMIT is an inlined integer (not $n): with a parameter, generic cached plans assume
    # 10% of rows are fetched and switch from ordered index scans to scan+sort.
    lim = int(limit) + 1
    sql = (
        f"SELECT {SELECT_COLS}, {sort.field.expr} AS sort_value FROM authors a "
        f"WHERE {qb.where_sql()} {order_by(sort, 'a.id')} LIMIT {lim}"
    )
    return sql, qb.params


def build_books_query(author_id: int, before_book_id: int | None, limit: int) -> tuple[str, list[Any]]:
    """Books of an author, newest first; keyset on book_id over author_books' PK."""
    params: list[Any] = [author_id]
    keyset = ""
    if before_book_id is not None:
        params.append(before_book_id)
        keyset = " AND ab.book_id < $2::bigint"
    sql = (
        f"SELECT {BOOK_SELECT_COLS}, x.roles FROM ("
        "SELECT ab.book_id, array_agg(ab.role ORDER BY ab.role) AS roles FROM author_books ab "
        f"WHERE ab.author_id = $1::bigint{keyset} GROUP BY ab.book_id"
        ") x JOIN books b ON b.id = x.book_id AND b.deleted_at IS NULL "
        f"ORDER BY x.book_id DESC LIMIT {int(limit) + 1}"
    )
    return sql, params


class AuthorRepository:
    async def find(
        self, conn: Connection, f: AuthorFilters, sort: Sort, cursor: Cursor | None, limit: int
    ) -> list[asyncpg.Record]:
        sql, params = build_list_query(f, sort, cursor, limit)
        return await conn.fetch(sql, *params)

    async def estimate(self, conn: Connection, f: AuthorFilters) -> int:
        qb = build_filters(f)
        return await explain_rows(conn, f"SELECT 1 FROM authors a WHERE {qb.where_sql()}", qb.params)

    async def get_card(self, conn: Connection, author_id: int) -> asyncpg.Record | None:
        return await conn.fetchrow(
            f"SELECT {SELECT_COLS}, cu.username AS created_by_username, uu.username AS updated_by_username "
            "FROM authors a LEFT JOIN users cu ON cu.id = a.created_by LEFT JOIN users uu ON uu.id = a.updated_by "
            "WHERE a.id = $1 AND a.deleted_at IS NULL",
            author_id,
        )

    async def exists_live(self, conn: Connection, author_id: int) -> bool:
        return bool(
            await conn.fetchval("SELECT EXISTS (SELECT 1 FROM authors WHERE id = $1 AND deleted_at IS NULL)", author_id)
        )

    async def exists_any(self, conn: Connection, author_id: int) -> bool:
        return bool(await conn.fetchval("SELECT EXISTS (SELECT 1 FROM authors WHERE id = $1)", author_id))

    async def live_ids(self, conn: Connection, ids: Sequence[int]) -> set[int]:
        rows = await conn.fetch("SELECT id FROM authors WHERE id = ANY($1::bigint[]) AND deleted_at IS NULL", list(ids))
        return {r["id"] for r in rows}

    async def books(
        self, conn: Connection, author_id: int, before_book_id: int | None, limit: int
    ) -> list[asyncpg.Record]:
        sql, params = build_books_query(author_id, before_book_id, limit)
        return await conn.fetch(sql, *params)

    async def lock(self, conn: Connection, author_id: int) -> asyncpg.Record | None:
        return await conn.fetchrow(
            f"SELECT {SELECT_COLS} FROM authors a WHERE a.id = $1 AND a.deleted_at IS NULL FOR UPDATE", author_id
        )

    async def insert(self, conn: Connection, values: dict[str, Any], user_id: int) -> asyncpg.Record:
        cols = [c for c in AUTHOR_COLUMNS if values.get(c) is not None]
        n = len(cols)
        placeholders = ", ".join(f"${i}" for i in range(1, n + 1))
        sql = (
            f"INSERT INTO authors AS a ({', '.join(cols)}, created_by, updated_by) "
            f"VALUES ({placeholders}, ${n + 1}, ${n + 1}) RETURNING {SELECT_COLS}"
        )
        row = await conn.fetchrow(sql, *[values[c] for c in cols], user_id)
        assert row is not None
        return row

    async def update(self, conn: Connection, author_id: int, changes: dict[str, Any], user_id: int) -> asyncpg.Record:
        sets = [f"{col} = ${i}" for i, col in enumerate(changes, start=3)]
        sets += ["version = a.version + 1", "updated_at = now()", "updated_by = $2"]
        row = await conn.fetchrow(
            f"UPDATE authors AS a SET {', '.join(sets)} WHERE a.id = $1 RETURNING {SELECT_COLS}",
            author_id,
            user_id,
            *changes.values(),
        )
        assert row is not None
        return row

    async def soft_delete(self, conn: Connection, author_id: int, user_id: int) -> None:
        await conn.execute(
            "UPDATE authors SET deleted_at = now(), updated_at = now(), updated_by = $2, version = version + 1 "
            "WHERE id = $1",
            author_id,
            user_id,
        )
