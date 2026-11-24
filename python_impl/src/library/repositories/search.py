"""Relevance search over books and authors: FTS (ts_rank) + trigram word similarity."""

from __future__ import annotations

from typing import Any, Literal

import asyncpg

from library.db import Connection

SearchType = Literal["books", "authors", "all"]

# Candidates are matched through GIN indexes (FTS or trigram), capped per type to bound the
# ranking cost; the cap is deterministic (newest ids first) so pagination stays stable.
_BOOKS = (
    "SELECT 'book'::text AS type, c.id, c.title, c.original_title AS subtitle, "
    "(ts_rank(c.search_vector, websearch_to_tsquery('simple', f_unaccent($1)))::float8 "
    " + word_similarity(f_unaccent($1), f_unaccent(c.title))::float8) AS score "
    "FROM (SELECT b.id, b.title, b.original_title, b.search_vector FROM books b "
    "      WHERE b.deleted_at IS NULL AND (b.search_vector @@ websearch_to_tsquery('simple', f_unaccent($1)) "
    "            OR f_unaccent($1) <% f_unaccent(b.title)) "
    "      ORDER BY b.id DESC LIMIT {cap}) c"
)
_AUTHORS = (
    "SELECT 'author'::text AS type, c.id, c.full_name AS title, c.original_name AS subtitle, "
    "(ts_rank(c.search_vector, websearch_to_tsquery('simple', f_unaccent($1)))::float8 "
    " + word_similarity(f_unaccent($1), f_unaccent(c.full_name || ' ' || coalesce(c.original_name, '')))::float8)"
    " AS score "
    "FROM (SELECT a.id, a.full_name, a.original_name, a.search_vector FROM authors a "
    "      WHERE a.deleted_at IS NULL AND (a.search_vector @@ websearch_to_tsquery('simple', f_unaccent($1)) "
    "            OR f_unaccent($1) <% f_unaccent(a.full_name || ' ' || coalesce(a.original_name, ''))) "
    "      ORDER BY a.id DESC LIMIT {cap}) c"
)


def build_search_query(
    q: str, type_: SearchType, after: tuple[float, int] | None, limit: int, max_candidates: int
) -> tuple[str, list[Any]]:
    parts = {"books": [_BOOKS], "authors": [_AUTHORS], "all": [_BOOKS, _AUTHORS]}[type_]
    cap = int(max_candidates)
    union = " UNION ALL ".join(f"({p.replace('{cap}', str(cap))})" for p in parts)
    params: list[Any] = [q]
    keyset = ""
    if after is not None:
        params += [after[0], after[1]]
        # Ids are globally unique (snowflake), so (score, id) is a total order across types.
        keyset = "WHERE (s.score, s.id) < ($2::float8, $3::bigint) "
    sql = (
        f"SELECT s.type, s.id, s.title, s.subtitle, s.score FROM ({union}) s "
        f"{keyset}ORDER BY s.score DESC, s.id DESC LIMIT {int(limit) + 1}"
    )
    return sql, params


class SearchRepository:
    async def search(
        self,
        conn: Connection,
        q: str,
        type_: SearchType,
        after: tuple[float, int] | None,
        limit: int,
        max_candidates: int,
    ) -> list[asyncpg.Record]:
        sql, params = build_search_query(q, type_, after, limit, max_candidates)
        return await conn.fetch(sql, *params)
