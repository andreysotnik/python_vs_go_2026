"""Author use-cases."""

from __future__ import annotations

from datetime import datetime
from typing import Any

from library.db import Database
from library.domain.cursor import decode_cursor
from library.domain.errors import NotFoundError
from library.domain.filters import AuthorFilters
from library.domain.models import (
    Author,
    AuthorBook,
    AuthorCard,
    AuthorCreate,
    AuthorPatch,
    AuthorReplace,
    HistoryEntry,
    Page,
)
from library.domain.sorting import AUTHOR_SORTS, parse_sort
from library.repositories.audit import AuditRepository
from library.repositories.authors import AUTHOR_COLUMNS, AuthorRepository
from library.repositories.books import BookRepository
from library.repositories.users import UserRepository
from library.services.books import _row_to_book
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

DEFAULT_SORT = "full_name"
BOOKS_SORT = "-book_id"
HISTORY_SORT = "changed_at"


def _column_values(fields: dict[str, Any]) -> dict[str, Any]:
    values = {c: fields.get(c) for c in AUTHOR_COLUMNS}
    values["external_ids"] = values["external_ids"] or {}
    return values


def _row_to_author(row: Any) -> dict[str, Any]:
    data = dict(row)
    data.pop("sort_value", None)
    return data


class AuthorService:
    def __init__(
        self,
        db: Database,
        authors: AuthorRepository,
        books: BookRepository,
        audit: AuditRepository,
        users: UserRepository,
        default_limit: int,
    ) -> None:
        self.db = db
        self.authors = authors
        self.books = books
        self.audit = audit
        self.users = users
        self.default_limit = default_limit

    async def find(
        self,
        filters: AuthorFilters,
        sort_raw: str | None,
        cursor_raw: str | None,
        limit: int | None,
        include_total: bool,
    ) -> Page[Author]:
        sort = parse_sort(sort_raw, AUTHOR_SORTS, DEFAULT_SORT)
        cursor = decode_cursor(cursor_raw, sort.key, sort.field.kind) if cursor_raw else None
        lim = clamp_limit(limit, self.default_limit)
        async with self.db.tx(readonly=True) as conn:
            rows = await self.authors.find(conn, filters, sort, cursor, lim)
            total = await self.authors.estimate(conn, filters) if include_total else None
        items, next_cursor = paginate(
            rows, lim, lambda r: Author.model_validate(_row_to_author(r)), sort.key, lambda r: r["sort_value"]
        )
        return Page[Author](
            items=items, next_cursor=next_cursor, limit=lim, total=total, total_is_estimate=total is not None
        )

    async def get(self, author_id: int) -> AuthorCard:
        async with self.db.tx(readonly=True) as conn:
            return await self._card(conn, author_id)

    async def _card(self, conn: Any, author_id: int) -> AuthorCard:
        row = await self.authors.get_card(conn, author_id)
        if row is None:
            raise NotFoundError(f"author {author_id} not found")
        return AuthorCard.model_validate(dict(row))

    async def books_of(self, author_id: int, cursor_raw: str | None, limit: int | None) -> Page[AuthorBook]:
        cursor = decode_cursor(cursor_raw, BOOKS_SORT, "int") if cursor_raw else None
        lim = clamp_limit(limit, self.default_limit)
        async with self.db.tx(readonly=True) as conn:
            if not await self.authors.exists_live(conn, author_id):
                raise NotFoundError(f"author {author_id} not found")
            rows = await self.authors.books(conn, author_id, cursor.id if cursor else None, lim)
            authors = await self.books.authors_of(conn, [r["id"] for r in rows[:lim]])

        def to_item(r: Any) -> AuthorBook:
            data = _row_to_book(r, authors.get(r["id"]))
            return AuthorBook.model_validate(data)

        items, next_cursor = paginate(rows, lim, to_item, BOOKS_SORT, lambda r: r["id"])
        return Page[AuthorBook](items=items, next_cursor=next_cursor, limit=lim, total=None, total_is_estimate=False)

    async def history(self, author_id: int, cursor_raw: str | None, limit: int | None) -> Page[HistoryEntry]:
        cursor = decode_cursor(cursor_raw, HISTORY_SORT, "timestamp") if cursor_raw else None
        lim = clamp_limit(limit, self.default_limit)
        after = (cursor.value, cursor.id) if cursor else None
        async with self.db.tx(readonly=True) as conn:
            if not await self.authors.exists_any(conn, author_id):
                raise NotFoundError(f"author {author_id} not found")
            rows = await self.audit.history(conn, "author", author_id, after, lim)  # type: ignore[arg-type]
        items, next_cursor = paginate(
            rows, lim, lambda r: HistoryEntry.model_validate(dict(r)), HISTORY_SORT, lambda r: r["changed_at"]
        )
        return Page[HistoryEntry](items=items, next_cursor=next_cursor, limit=lim, total=None, total_is_estimate=False)

    async def create(self, data: AuthorCreate, user_id: int) -> AuthorCard:
        values = _column_values(data.model_dump())
        async with self.db.tx() as conn:
            await ensure_user(conn, self.users, user_id)
            row = await self.authors.insert(conn, values, user_id)
            await self.audit.record(conn, "author", row["id"], "create", user_id, snapshot(dict(row), AUTHOR_COLUMNS))
            return await self._card(conn, row["id"])

    async def replace(self, author_id: int, data: AuthorReplace, if_match: int | None, user_id: int) -> AuthorCard:
        expected = resolve_version(if_match, data.version)
        return await self._apply(author_id, _column_values(data.model_dump(exclude={"version"})), expected, user_id)

    async def patch(self, author_id: int, data: AuthorPatch, if_match: int | None, user_id: int) -> AuthorCard:
        expected = resolve_version(if_match, data.version)
        patch = data.model_dump(exclude_unset=True, exclude={"version"})
        return await self._apply(author_id, patch, expected, user_id, partial=True)

    async def _apply(
        self, author_id: int, target: dict[str, Any], expected: int | None, user_id: int, *, partial: bool = False
    ) -> AuthorCard:
        async with self.db.tx() as conn:
            await ensure_user(conn, self.users, user_id)
            current = await self.authors.lock(conn, author_id)
            if current is None:
                raise NotFoundError(f"author {author_id} not found")
            check_version(current["version"], expected, "author", author_id)
            cur = dict(current)
            if partial:
                merged = {c: cur[c] for c in AUTHOR_COLUMNS}
                merged.update(target)
                target = _column_values(validate_model(AuthorReplace, jsonable(merged)).model_dump())
            changes = changes_of(cur, target, AUTHOR_COLUMNS)
            if changes:
                await self.authors.update(conn, author_id, changes, user_id)
                await self.audit.record(conn, "author", author_id, "update", user_id, diff_of(cur, changes))
            return await self._card(conn, author_id)

    async def delete(self, author_id: int, if_match: int | None, user_id: int) -> None:
        expected = resolve_version(if_match, None, required=False)
        async with self.db.tx() as conn:
            await ensure_user(conn, self.users, user_id)
            current = await self.authors.lock(conn, author_id)
            if current is None:
                raise NotFoundError(f"author {author_id} not found")
            check_version(current["version"], expected, "author", author_id)
            await self.authors.soft_delete(conn, author_id, user_id)
            await self.audit.record(
                conn, "author", author_id, "delete", user_id, {"deleted_at": jsonable(datetime.now().astimezone())}
            )
