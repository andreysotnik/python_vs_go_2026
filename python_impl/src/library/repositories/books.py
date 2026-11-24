"""Books persistence: list/keyset queries, card, writes, author links."""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

import asyncpg

from library.db import Connection
from library.domain.cursor import Cursor
from library.domain.filters import BookFilters
from library.domain.sorting import Sort
from library.repositories.common import explain_rows
from library.repositories.sql import QueryBuilder, keyset_clause, like_escape, order_by, prefix_tsquery

BOOK_COLUMNS = (
    "title",
    "original_title",
    "subtitle",
    "description",
    "isbn_10",
    "isbn_13",
    "language_code",
    "publication_year",
    "publisher",
    "page_count",
    "genres",
    "edition",
    "external_ids",
)
SELECT_COLS = (
    "b.id, "
    + ", ".join(f"b.{c}" for c in BOOK_COLUMNS)
    + ", b.created_at, b.created_by, b.updated_at, b.updated_by, b.version"
)


def build_filters(f: BookFilters) -> QueryBuilder:
    qb = QueryBuilder().where("b.deleted_at IS NULL")
    if f.q:
        qb.where("b.search_vector @@ websearch_to_tsquery('simple', f_unaccent({}))", f.q)
    if f.title:
        # Substring/fuzzy match served by the trigram GIN index books_title_trgm_idx.
        qb.where("f_unaccent(b.title) ILIKE '%' || f_unaccent({}) || '%'", like_escape(f.title))
    if f.isbn13:
        # Single-partition probe into the ISBN registry instead of 64 index probes on books.
        qb.where("b.id = (SELECT bi.book_id FROM book_isbns bi WHERE bi.isbn_13 = {})", f.isbn13)
    if f.author_id is not None:
        qb.where("b.id IN (SELECT ab.book_id FROM author_books ab WHERE ab.author_id = {}::bigint)", f.author_id)
    if f.author:
        tsq = prefix_tsquery(f.author) or "''"
        qb.where(
            "b.id IN (SELECT ab.book_id FROM author_books ab WHERE ab.author_id IN ("
            "SELECT a.id FROM authors a WHERE a.deleted_at IS NULL "
            "AND a.search_vector @@ to_tsquery('simple', f_unaccent({}))))",
            tsq,
        )
    if f.language:
        qb.where("b.language_code = {}", f.language)
    if f.year_from is not None:
        qb.where("coalesce(b.publication_year, 32767::smallint) >= {}::smallint", f.year_from)
        qb.where("b.publication_year IS NOT NULL")
    if f.year_to is not None:
        qb.where("coalesce(b.publication_year, 32767::smallint) <= {}::smallint", f.year_to)
    if f.publisher:
        qb.where("b.publisher = {}", f.publisher)
    if f.genre:
        qb.where("b.genres @> ARRAY[{}]::text[]", f.genre)
    if f.created_by is not None:
        qb.where("b.created_by = {}::bigint", f.created_by)
    if f.created_from is not None:
        qb.where("b.created_at >= {}", f.created_from)
    if f.created_to is not None:
        qb.where("b.created_at <= {}", f.created_to)
    return qb


def build_list_query(f: BookFilters, sort: Sort, cursor: Cursor | None, limit: int) -> tuple[str, list[Any]]:
    qb = build_filters(f)
    keyset_clause(qb, sort, cursor, "b.id")
    # LIMIT is an inlined integer (not $n): with a parameter, generic cached plans assume
    # 10% of rows are fetched and switch from ordered index scans to scan+sort.
    lim = int(limit) + 1
    sql = (
        f"SELECT {SELECT_COLS}, {sort.field.expr} AS sort_value FROM books b "
        f"WHERE {qb.where_sql()} {order_by(sort, 'b.id')} LIMIT {lim}"
    )
    return sql, qb.params


class BookRepository:
    async def find(
        self, conn: Connection, f: BookFilters, sort: Sort, cursor: Cursor | None, limit: int
    ) -> list[asyncpg.Record]:
        sql, params = build_list_query(f, sort, cursor, limit)
        return await conn.fetch(sql, *params)

    async def estimate(self, conn: Connection, f: BookFilters) -> int:
        qb = build_filters(f)
        return await explain_rows(conn, f"SELECT 1 FROM books b WHERE {qb.where_sql()}", qb.params)

    async def authors_of(self, conn: Connection, book_ids: Sequence[int]) -> dict[int, list[asyncpg.Record]]:
        if not book_ids:
            return {}
        rows = await conn.fetch(
            "SELECT ba.book_id, a.id, a.full_name, ba.role, ba.ordinal "
            "FROM book_authors ba JOIN authors a ON a.id = ba.author_id AND a.deleted_at IS NULL "
            "WHERE ba.book_id = ANY($1::bigint[]) ORDER BY ba.book_id, ba.ordinal, a.id",
            list(book_ids),
        )
        out: dict[int, list[asyncpg.Record]] = {}
        for row in rows:
            out.setdefault(row["book_id"], []).append(row)
        return out

    async def get_card(self, conn: Connection, book_id: int) -> asyncpg.Record | None:
        return await conn.fetchrow(
            f"SELECT {SELECT_COLS}, cu.username AS created_by_username, uu.username AS updated_by_username "
            "FROM books b LEFT JOIN users cu ON cu.id = b.created_by LEFT JOIN users uu ON uu.id = b.updated_by "
            "WHERE b.id = $1 AND b.deleted_at IS NULL",
            book_id,
        )

    async def exists_any(self, conn: Connection, book_id: int) -> bool:
        """True if the book ever existed (including soft-deleted) - for history."""
        return bool(await conn.fetchval("SELECT EXISTS (SELECT 1 FROM books WHERE id = $1)", book_id))

    async def lock(self, conn: Connection, book_id: int) -> asyncpg.Record | None:
        return await conn.fetchrow(
            f"SELECT {SELECT_COLS} FROM books b WHERE b.id = $1 AND b.deleted_at IS NULL FOR UPDATE", book_id
        )

    async def insert(self, conn: Connection, values: dict[str, Any], user_id: int) -> asyncpg.Record:
        cols = [c for c in BOOK_COLUMNS if values.get(c) is not None]
        placeholders = ", ".join(f"${i}" for i in range(1, len(cols) + 1))
        n = len(cols)
        sql = (
            f"INSERT INTO books AS b ({', '.join(cols)}{', ' if cols else ''}created_by, updated_by) "
            f"VALUES ({placeholders}{', ' if cols else ''}${n + 1}, ${n + 1}) RETURNING {SELECT_COLS}"
        )
        row = await conn.fetchrow(sql, *[values[c] for c in cols], user_id)
        assert row is not None
        return row

    async def update(self, conn: Connection, book_id: int, changes: dict[str, Any], user_id: int) -> asyncpg.Record:
        sets = [f"{col} = ${i}" for i, col in enumerate(changes, start=3)]
        sets += ["version = b.version + 1", "updated_at = now()", "updated_by = $2"]
        row = await conn.fetchrow(
            f"UPDATE books AS b SET {', '.join(sets)} WHERE b.id = $1 RETURNING {SELECT_COLS}",
            book_id,
            user_id,
            *changes.values(),
        )
        assert row is not None
        return row

    async def soft_delete(self, conn: Connection, book_id: int, user_id: int) -> None:
        await conn.execute(
            "UPDATE books SET deleted_at = now(), updated_at = now(), updated_by = $2, version = version + 1 "
            "WHERE id = $1",
            book_id,
            user_id,
        )

    async def claim_isbn(self, conn: Connection, isbn13: str, book_id: int) -> bool:
        """Register an ISBN globally; False if another book already owns it."""
        status = await conn.execute(
            "INSERT INTO book_isbns (isbn_13, book_id) VALUES ($1, $2) ON CONFLICT (isbn_13) DO NOTHING",
            isbn13,
            book_id,
        )
        return status.endswith(" 1")

    async def release_isbn(self, conn: Connection, isbn13: str, book_id: int) -> None:
        await conn.execute("DELETE FROM book_isbns WHERE isbn_13 = $1 AND book_id = $2", isbn13, book_id)

    async def replace_links(
        self, conn: Connection, book_id: int, links: Sequence[tuple[int, str, int]], *, existing: bool
    ) -> None:
        """Write both directions of the relation in the same transaction."""
        if existing:
            old = await conn.fetch("SELECT author_id FROM book_authors WHERE book_id = $1", book_id)
            await conn.execute("DELETE FROM book_authors WHERE book_id = $1", book_id)
            if old:
                await conn.execute(
                    "DELETE FROM author_books WHERE author_id = ANY($1::bigint[]) AND book_id = $2",
                    [r["author_id"] for r in old],
                    book_id,
                )
        if not links:
            return
        author_ids = [link[0] for link in links]
        roles = [link[1] for link in links]
        ordinals = [link[2] for link in links]
        await conn.execute(
            "INSERT INTO book_authors (book_id, author_id, role, ordinal) "
            "SELECT $1, t.a, t.r, t.o FROM unnest($2::bigint[], $3::text[], $4::smallint[]) AS t(a, r, o)",
            book_id,
            author_ids,
            roles,
            ordinals,
        )
        await conn.execute(
            "INSERT INTO author_books (author_id, book_id, role, ordinal) "
            "SELECT t.a, $1, t.r, t.o FROM unnest($2::bigint[], $3::text[], $4::smallint[]) AS t(a, r, o)",
            book_id,
            author_ids,
            roles,
            ordinals,
        )

    async def link_tuples(self, conn: Connection, book_id: int) -> list[tuple[int, str, int]]:
        rows = await conn.fetch(
            "SELECT author_id, role, ordinal FROM book_authors WHERE book_id = $1 ORDER BY ordinal, author_id, role",
            book_id,
        )
        return [(r["author_id"], r["role"], r["ordinal"]) for r in rows]
