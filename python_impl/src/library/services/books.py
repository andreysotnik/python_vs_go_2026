"""Book use-cases: listing, cards, writes with optimistic locking and auditing."""

from __future__ import annotations

from datetime import datetime
from typing import Any

import asyncpg

from library.db import Database
from library.domain import isbn as isbn_lib
from library.domain.cursor import decode_cursor
from library.domain.errors import ConflictError, NotFoundError, ValidationFailedError
from library.domain.filters import BookFilters
from library.domain.models import (
    Book,
    BookAuthor,
    BookAuthorLink,
    BookCard,
    BookCreate,
    BookPatch,
    BookReplace,
    HistoryEntry,
    Page,
)
from library.domain.sorting import BOOK_SORTS, parse_sort
from library.repositories.audit import AuditRepository
from library.repositories.authors import AuthorRepository
from library.repositories.books import BOOK_COLUMNS, BookRepository
from library.repositories.users import UserRepository
from library.services.common import (
    changes_of,
    check_version,
    clamp_limit,
    diff_of,
    ensure_user,
    jsonable,
    paginate,
    resolve_version,
    snapshot,
    validate_model,
)

DEFAULT_SORT = "-created_at"
HISTORY_SORT = "changed_at"


def _row_to_book(row: asyncpg.Record, authors: list[asyncpg.Record] | None = None) -> dict[str, Any]:
    data = dict(row)
    data.pop("sort_value", None)
    data["authors"] = [
        BookAuthor(id=a["id"], full_name=a["full_name"], role=a["role"], ordinal=a["ordinal"]) for a in authors or []
    ]
    return data


def _column_values(fields: dict[str, Any]) -> dict[str, Any]:
    values = {c: fields.get(c) for c in BOOK_COLUMNS}
    values["genres"] = values["genres"] or []
    values["external_ids"] = values["external_ids"] or {}
    return values


def _links(links: list[BookAuthorLink] | None) -> list[tuple[int, str, int]]:
    return sorted(((link.author_id, link.role, link.ordinal) for link in links or []), key=lambda t: (t[2], t[0], t[1]))


def _links_json(links: list[tuple[int, str, int]]) -> list[dict[str, Any]]:
    return [{"author_id": str(a), "role": r, "ordinal": o} for a, r, o in links]


class BookService:
    def __init__(
        self,
        db: Database,
        books: BookRepository,
        authors: AuthorRepository,
        audit: AuditRepository,
        users: UserRepository,
        default_limit: int,
    ) -> None:
        self.db = db
        self.books = books
        self.authors = authors
        self.audit = audit
        self.users = users
        self.default_limit = default_limit

    # ------------------------------------------------------------ reads

    async def find(
        self,
        filters: BookFilters,
        sort_raw: str | None,
        cursor_raw: str | None,
        limit: int | None,
        include_total: bool,
    ) -> Page[Book]:
        sort = parse_sort(sort_raw, BOOK_SORTS, DEFAULT_SORT)
        cursor = decode_cursor(cursor_raw, sort.key, sort.field.kind) if cursor_raw else None
        lim = clamp_limit(limit, self.default_limit)
        async with self.db.tx(readonly=True) as conn:
            rows = await self.books.find(conn, filters, sort, cursor, lim)
            authors = await self.books.authors_of(conn, [r["id"] for r in rows[:lim]])
            total = await self.books.estimate(conn, filters) if include_total else None
        items, next_cursor = paginate(
            rows,
            lim,
            lambda r: Book.model_validate(_row_to_book(r, authors.get(r["id"]))),
            sort.key,
            lambda r: r["sort_value"],
        )
        return Page[Book](
            items=items, next_cursor=next_cursor, limit=lim, total=total, total_is_estimate=total is not None
        )

    async def get(self, book_id: int) -> BookCard:
        async with self.db.tx(readonly=True) as conn:
            return await self._card(conn, book_id)

    async def _card(self, conn: Any, book_id: int) -> BookCard:
        row = await self.books.get_card(conn, book_id)
        if row is None:
            raise NotFoundError(f"book {book_id} not found")
        authors = await self.books.authors_of(conn, [book_id])
        return BookCard.model_validate(_row_to_book(row, authors.get(book_id)))

    async def history(self, book_id: int, cursor_raw: str | None, limit: int | None) -> Page[HistoryEntry]:
        cursor = decode_cursor(cursor_raw, HISTORY_SORT, "timestamp") if cursor_raw else None
        lim = clamp_limit(limit, self.default_limit)
        after = (cursor.value, cursor.id) if cursor else None
        async with self.db.tx(readonly=True) as conn:
            if not await self.books.exists_any(conn, book_id):
                raise NotFoundError(f"book {book_id} not found")
            rows = await self.audit.history(conn, "book", book_id, after, lim)  # type: ignore[arg-type]
        items, next_cursor = paginate(
            rows, lim, lambda r: HistoryEntry.model_validate(dict(r)), HISTORY_SORT, lambda r: r["changed_at"]
        )
        return Page[HistoryEntry](items=items, next_cursor=next_cursor, limit=lim, total=None, total_is_estimate=False)

    # ------------------------------------------------------------ writes

    async def _check_authors(self, conn: Any, links: list[tuple[int, str, int]]) -> None:
        ids = {a for a, _, _ in links}
        if not ids:
            return
        missing = ids - await self.authors.live_ids(conn, list(ids))
        if missing:
            raise ValidationFailedError(
                "unknown authors",
                errors=[
                    {"loc": ["body", "authors"], "msg": f"author {m} does not exist", "type": "unknown_author"}
                    for m in sorted(missing)
                ],
            )

    async def _claim_isbn(self, conn: Any, isbn13: str, book_id: int) -> None:
        if not await self.books.claim_isbn(conn, isbn13, book_id):
            raise ConflictError(f"a book with ISBN-13 {isbn13} already exists")

    async def create(self, data: BookCreate, user_id: int) -> BookCard:
        values = _column_values(data.model_dump(exclude={"authors"}))
        links = _links(data.authors)
        async with self.db.tx() as conn:
            await ensure_user(conn, self.users, user_id)
            await self._check_authors(conn, links)
            row = await self.books.insert(conn, values, user_id)
            book_id: int = row["id"]
            if values["isbn_13"]:
                await self._claim_isbn(conn, values["isbn_13"], book_id)
            await self.books.replace_links(conn, book_id, links, existing=False)
            diff = snapshot(dict(row), BOOK_COLUMNS)
            if links:
                diff["authors"] = _links_json(links)
            await self.audit.record(conn, "book", book_id, "create", user_id, diff)
            return await self._card(conn, book_id)

    async def replace(self, book_id: int, data: BookReplace, if_match: int | None, user_id: int) -> BookCard:
        expected = resolve_version(if_match, data.version)
        target = _column_values(data.model_dump(exclude={"authors", "version"}))
        return await self._apply(book_id, target, _links(data.authors), expected, user_id)

    async def patch(self, book_id: int, data: BookPatch, if_match: int | None, user_id: int) -> BookCard:
        expected = resolve_version(if_match, data.version)
        patch = data.model_dump(exclude_unset=True, exclude={"version", "authors"})
        links = _links(data.authors) if "authors" in data.model_fields_set else None
        return await self._apply(book_id, patch, links, expected, user_id, partial=True)

    async def _apply(
        self,
        book_id: int,
        target: dict[str, Any],
        links: list[tuple[int, str, int]] | None,
        expected: int | None,
        user_id: int,
        *,
        partial: bool = False,
    ) -> BookCard:
        async with self.db.tx() as conn:
            await ensure_user(conn, self.users, user_id)
            current = await self.books.lock(conn, book_id)
            if current is None:
                raise NotFoundError(f"book {book_id} not found")
            check_version(current["version"], expected, "book", book_id)
            cur = dict(current)
            if partial:
                merged = {c: cur[c] for c in BOOK_COLUMNS}
                if "isbn_13" in target and "isbn_10" not in target:
                    merged["isbn_10"] = None
                if "isbn_10" in target and "isbn_13" not in target:
                    merged["isbn_13"] = None
                merged.update(target)
                target = _column_values(validate_model(BookReplace, jsonable(merged)).model_dump())
            changes = changes_of(cur, target, BOOK_COLUMNS)
            old_links = await self.books.link_tuples(conn, book_id)
            links_changed = links is not None and links != old_links
            if links_changed:
                assert links is not None
                await self._check_authors(conn, links)
            if not changes and not links_changed:
                return await self._card(conn, book_id)
            if "isbn_13" in changes:
                if cur["isbn_13"]:
                    await self.books.release_isbn(conn, cur["isbn_13"], book_id)
                if changes["isbn_13"]:
                    await self._claim_isbn(conn, changes["isbn_13"], book_id)
            diff = diff_of(cur, changes)
            await self.books.update(conn, book_id, changes, user_id)
            if links_changed:
                assert links is not None
                await self.books.replace_links(conn, book_id, links, existing=True)
                diff["authors"] = {"old": _links_json(old_links), "new": _links_json(links)}
            await self.audit.record(conn, "book", book_id, "update", user_id, diff)
            return await self._card(conn, book_id)

    async def delete(self, book_id: int, if_match: int | None, user_id: int) -> None:
        expected = resolve_version(if_match, None, required=False)
        async with self.db.tx() as conn:
            await ensure_user(conn, self.users, user_id)
            current = await self.books.lock(conn, book_id)
            if current is None:
                raise NotFoundError(f"book {book_id} not found")
            check_version(current["version"], expected, "book", book_id)
            await self.books.soft_delete(conn, book_id, user_id)
            if current["isbn_13"]:
                await self.books.release_isbn(conn, current["isbn_13"], book_id)
            await self.audit.record(
                conn, "book", book_id, "delete", user_id, {"deleted_at": jsonable(datetime.now().astimezone())}
            )


def normalize_isbn_filter(raw: str | None) -> str | None:
    if raw is None:
        return None
    try:
        return isbn_lib.to_isbn13(raw)
    except ValueError as exc:
        raise ValidationFailedError(
            "invalid isbn filter",
            errors=[{"loc": ["query", "isbn"], "msg": str(exc), "type": "isbn"}],
        ) from exc
