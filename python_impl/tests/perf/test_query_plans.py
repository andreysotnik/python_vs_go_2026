"""EXPLAIN (ANALYZE, BUFFERS) of the key API queries on a *seeded* database.

Asserts that no plan contains a Seq Scan over books/authors (or their partitions) and that no
query uses OFFSET; writes the plans to docs/QUERY_PLANS.md.

Run: PERF_DATABASE_URL=postgresql://library:library@localhost:15432/library pytest tests/perf
"""

from __future__ import annotations

import json
import os
import re
from collections.abc import AsyncIterator
from datetime import UTC, date, datetime, timedelta
from pathlib import Path
from typing import Any

import asyncpg
import pytest

from library.domain.cursor import Cursor
from library.domain.filters import AuthorFilters, BookFilters
from library.domain.sorting import AUTHOR_SORTS, BOOK_SORTS, Sort
from library.repositories import authors as author_repo
from library.repositories import books as book_repo
from library.repositories.search import build_search_query

pytestmark = pytest.mark.perf
DSN = os.environ.get("PERF_DATABASE_URL")
ROOT = Path(__file__).resolve().parents[2]
BIG_TABLE = re.compile(r"^(books|authors)(_p\d+)?$")
MIN_BOOKS = 100_000
RESULTS: list[dict[str, Any]] = []


@pytest.fixture(scope="module")
async def conn() -> AsyncIterator[asyncpg.Connection]:  # type: ignore[type-arg]
    if not DSN:
        pytest.skip("PERF_DATABASE_URL is not set (needs a seeded database)")
    c = await asyncpg.connect(DSN)
    n = await c.fetchval("SELECT sum(reltuples)::bigint FROM pg_class WHERE relname ~ '^books_p[0-9]+$'")
    if not n or n < MIN_BOOKS:
        await c.close()
        pytest.skip(f"database is not seeded (books ~ {n})")
    await c.execute("SET statement_timeout = '30s'")
    yield c
    await c.close()


@pytest.fixture(scope="module")
async def samples(conn: asyncpg.Connection) -> dict[str, Any]:  # type: ignore[type-arg]
    s: dict[str, Any] = {}
    s["prolific_author"] = await conn.fetchval(
        "SELECT author_id FROM author_books_p00 GROUP BY author_id ORDER BY count(*) DESC LIMIT 1"
    )
    s["author"] = await conn.fetchrow(
        "SELECT id, full_name FROM authors WHERE original_name IS NOT NULL LIMIT 1 OFFSET 500"
    )
    s["book"] = await conn.fetchrow(
        "SELECT id, isbn_13, title, publisher, created_by, created_at FROM books_p07 "
        "WHERE isbn_13 IS NOT NULL AND publisher IS NOT NULL LIMIT 1 OFFSET 1000"
    )
    s["popular_publisher"] = await conn.fetchval(
        "SELECT publisher FROM books_p01 WHERE publisher IS NOT NULL GROUP BY 1 ORDER BY count(*) DESC LIMIT 1"
    )
    s["mid_title"] = await conn.fetchrow("SELECT title, id FROM books_p03 ORDER BY title, id LIMIT 1 OFFSET 7000")
    s["mid_created"] = await conn.fetchrow(
        "SELECT created_at, id FROM books_p03 ORDER BY created_at, id LIMIT 1 OFFSET 7000"
    )
    s["mid_name"] = await conn.fetchrow(
        "SELECT full_name, id FROM authors_p03 ORDER BY full_name, id LIMIT 1 OFFSET 3000"
    )
    return s


def _walk(node: dict[str, Any]) -> list[dict[str, Any]]:
    out = [node]
    for child in node.get("Plans", []):
        out.extend(_walk(child))
    return out


async def _explain(conn: asyncpg.Connection, name: str, sql: str, params: list[Any]) -> None:  # type: ignore[type-arg]
    assert "OFFSET" not in sql.upper(), f"{name}: OFFSET is forbidden"
    # Warm-up (the API runs on a warm cache too), then measure.
    await conn.fetch(sql, *params)
    plan_json = await conn.fetchval(f"EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) {sql}", *params)
    plan = json.loads(plan_json)[0] if isinstance(plan_json, str) else plan_json[0]
    text_rows = await conn.fetch(f"EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) {sql}", *params)
    text = "\n".join(r[0] for r in text_rows)
    nodes = _walk(plan["Plan"])
    seq = sorted({n.get("Relation Name", "") for n in nodes if n["Node Type"] == "Seq Scan"})
    bad = [r for r in seq if BIG_TABLE.match(r)]
    RESULTS.append(
        {
            "name": name,
            "sql": sql,
            "params": params,
            "ms": plan["Execution Time"],
            "rows": plan["Plan"].get("Actual Rows"),
            "seq": seq,
            "ok": not bad,
            "plan": text,
        }
    )
    assert not bad, f"{name}: Seq Scan on {bad}\n{text}"


def _b(sort: str) -> Sort:
    return Sort(BOOK_SORTS[sort.lstrip("-")], sort.startswith("-"))


def _a(sort: str) -> Sort:
    return Sort(AUTHOR_SORTS[sort.lstrip("-")], sort.startswith("-"))


async def test_book_lists(conn: asyncpg.Connection, samples: dict[str, Any]) -> None:  # type: ignore[type-arg]
    s = samples
    mt, mc = s["mid_title"], s["mid_created"]
    cases: list[tuple[str, BookFilters, str, Cursor | None]] = [
        ("books: default (-created_at), first page", BookFilters(), "-created_at", None),
        (
            "books: -created_at, deep cursor",
            BookFilters(),
            "-created_at",
            Cursor("-created_at", mc["created_at"], mc["id"]),
        ),
        ("books: title asc, first page", BookFilters(), "title", None),
        ("books: title asc, deep cursor", BookFilters(), "title", Cursor("title", mt["title"], mt["id"])),
        ("books: -title, deep cursor", BookFilters(), "-title", Cursor("-title", mt["title"], mt["id"])),
        ("books: publication_year asc", BookFilters(), "publication_year", None),
        (
            "books: -publication_year, cursor",
            BookFilters(),
            "-publication_year",
            Cursor("-publication_year", 1999, s["book"]["id"]),
        ),
        ("books: language=ru", BookFilters(language="ru"), "-created_at", None),
        ("books: language=en, title", BookFilters(language="en"), "title", None),
        ("books: publisher (popular), title", BookFilters(publisher=s["popular_publisher"]), "title", None),
        ("books: publisher (exact), -created_at", BookFilters(publisher=s["book"]["publisher"]), "-created_at", None),
        ("books: genre=poetry", BookFilters(genre="poetry"), "-created_at", None),
        (
            "books: year range 1900..1950, year sort",
            BookFilters(year_from=1900, year_to=1950),
            "publication_year",
            None,
        ),
        ("books: year range + language", BookFilters(year_from=2000, year_to=2010, language="de"), "-created_at", None),
        ("books: created_by", BookFilters(created_by=s["book"]["created_by"]), "-created_at", None),
        (
            "books: created_from/created_to (1 day)",
            BookFilters(created_from=s["book"]["created_at"], created_to=s["book"]["created_at"] + timedelta(days=1)),
            "created_at",
            None,
        ),
        ("books: isbn", BookFilters(isbn13=s["book"]["isbn_13"]), "-created_at", None),
        ("books: author_id (most prolific author)", BookFilters(author_id=s["prolific_author"]), "-created_at", None),
        ("books: author_id + title sort", BookFilters(author_id=s["prolific_author"]), "title", None),
        ("books: author name", BookFilters(author=s["author"]["full_name"]), "-created_at", None),
        ("books: title substring (trigram)", BookFilters(title="shadow riv"), "-created_at", None),
        ("books: q full-text", BookFilters(q="война мир"), "-created_at", None),
        ("books: q full-text + language + year", BookFilters(q="garden", language="en", year_from=1990), "title", None),
    ]
    for name, f, sort, cursor in cases:
        sql, params = book_repo.build_list_query(f, _b(sort), cursor, 20)
        await _explain(conn, name, sql, params)


async def test_author_lists(conn: asyncpg.Connection, samples: dict[str, Any]) -> None:  # type: ignore[type-arg]
    mn = samples["mid_name"]
    cases: list[tuple[str, AuthorFilters, str, Cursor | None]] = [
        ("authors: full_name, first page", AuthorFilters(), "full_name", None),
        (
            "authors: full_name, deep cursor",
            AuthorFilters(),
            "full_name",
            Cursor("full_name", mn["full_name"], mn["id"]),
        ),
        ("authors: birth_date", AuthorFilters(), "birth_date", None),
        (
            "authors: -birth_date, cursor",
            AuthorFilters(),
            "-birth_date",
            Cursor("-birth_date", date(1900, 1, 1), mn["id"]),
        ),
        ("authors: -created_at", AuthorFilters(), "-created_at", None),
        ("authors: country=JP", AuthorFilters(country="JP"), "full_name", None),
        (
            "authors: born range",
            AuthorFilters(born_from=date(1850, 1, 1), born_to=date(1860, 1, 1)),
            "birth_date",
            None,
        ),
        ("authors: name substring (trigram, cyrillic)", AuthorFilters(name="Михаил"), "full_name", None),
        ("authors: q full-text", AuthorFilters(q=samples["author"]["full_name"]), "full_name", None),
    ]
    for name, f, sort, cursor in cases:
        sql, params = author_repo.build_list_query(f, _a(sort), cursor, 20)
        await _explain(conn, name, sql, params)


async def test_point_queries(conn: asyncpg.Connection, samples: dict[str, Any]) -> None:  # type: ignore[type-arg]
    book_id = samples["book"]["id"]
    sql, params = author_repo.build_books_query(samples["prolific_author"], None, 20)
    await _explain(conn, "author books (most prolific), first page", sql, params)
    sql, params = author_repo.build_books_query(samples["prolific_author"], book_id, 20)
    await _explain(conn, "author books, cursor", sql, params)
    await _explain(
        conn,
        "book card",
        f"SELECT {book_repo.SELECT_COLS}, cu.username FROM books b LEFT JOIN users cu ON cu.id = b.created_by "
        "WHERE b.id = $1 AND b.deleted_at IS NULL",
        [book_id],
    )
    await _explain(
        conn,
        "authors of books (list page enrichment)",
        "SELECT ba.book_id, a.id, a.full_name, ba.role, ba.ordinal "
        "FROM book_authors ba JOIN authors a ON a.id = ba.author_id AND a.deleted_at IS NULL "
        "WHERE ba.book_id = ANY($1::bigint[]) ORDER BY ba.book_id, ba.ordinal, a.id",
        [[book_id, samples["mid_title"]["id"], samples["mid_created"]["id"]]],
    )
    await _explain(
        conn,
        "book history",
        "SELECT al.id, al.action, al.changed_at FROM audit_log al WHERE al.entity_type = 'book' AND al.entity_id = $1 "
        "ORDER BY al.changed_at, al.id LIMIT 21",
        [book_id],
    )


async def test_search(conn: asyncpg.Connection, samples: dict[str, Any]) -> None:  # type: ignore[type-arg]
    for name, q, typ in [
        ("search all: author name", samples["author"]["full_name"], "all"),
        ("search books: typo 'shadw river'", "shadw river", "books"),
        ("search authors: cyrillic", "Михаил Каров", "authors"),
    ]:
        sql, params = build_search_query(q, typ, None, 20, 1000)  # type: ignore[arg-type]
        await _explain(conn, name, sql, params)


def teardown_module() -> None:
    if not RESULTS:
        return
    lines = [
        "# Query plans (EXPLAIN ANALYZE, BUFFERS) on the seeded database",
        "",
        f"Generated by `make explain` (tests/perf/test_query_plans.py) at {datetime.now(UTC):%Y-%m-%d %H:%M} UTC.",
        "Check: no `Seq Scan` on `books`/`authors` or their partitions, no `OFFSET`. Cache is warm (one warm-up run).",
        "",
        "| # | Query | Exec ms | Rows | Seq Scans (any table) | Result |",
        "|---|-------|--------:|-----:|-----------------------|--------|",
    ]
    for i, r in enumerate(RESULTS, 1):
        seq = ", ".join(r["seq"]) or "-"
        lines.append(f"| {i} | {r['name']} | {r['ms']:.2f} | {r['rows']} | {seq} | {'PASS' if r['ok'] else 'FAIL'} |")
    for i, r in enumerate(RESULTS, 1):
        params = [str(p) for p in r["params"]]
        lines += [
            "",
            f"## {i}. {r['name']}",
            "",
            "```sql",
            r["sql"],
            "```",
            f"Params: `{params}`",
            "",
            "```",
            r["plan"],
            "```",
        ]
    (ROOT / "docs" / "QUERY_PLANS.md").write_text("\n".join(lines) + "\n")
