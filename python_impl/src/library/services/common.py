"""Helpers shared by services: pagination, version checks, audit diffs."""

from __future__ import annotations

from collections.abc import Callable, Iterable, Mapping, Sequence
from datetime import date, datetime
from typing import Any

import asyncpg
from pydantic import BaseModel, ValidationError

from library.db import Connection
from library.domain.cursor import CursorValue, encode_cursor
from library.domain.errors import (
    ConflictError,
    PreconditionRequiredError,
    UnauthorizedError,
    ValidationFailedError,
)
from library.repositories.users import UserRepository

MAX_LIMIT = 100


def clamp_limit(limit: int | None, default: int) -> int:
    if limit is None:
        return default
    return max(1, min(MAX_LIMIT, limit))


def paginate[T](
    rows: Sequence[asyncpg.Record],
    limit: int,
    to_item: Callable[[asyncpg.Record], T],
    sort_key: str,
    cursor_value: Callable[[asyncpg.Record], CursorValue],
) -> tuple[list[T], str | None]:
    """rows were fetched with LIMIT limit+1; the extra row only signals that a next page exists."""
    has_more = len(rows) > limit
    page = rows[:limit]
    next_cursor = None
    if has_more and page:
        last = page[-1]
        next_cursor = encode_cursor(sort_key, cursor_value(last), last["id"])
    return [to_item(r) for r in page], next_cursor


def resolve_version(if_match: int | None, body_version: int | None, *, required: bool = True) -> int | None:
    expected = if_match if if_match is not None else body_version
    if expected is None and required:
        raise PreconditionRequiredError(
            "optimistic locking: send the current version in If-Match header (or `version` in the body)"
        )
    return expected


def check_version(current: int, expected: int | None, entity: str, entity_id: int) -> None:
    if expected is not None and expected != current:
        raise ConflictError(
            f"{entity} {entity_id} was modified concurrently: expected version {expected}, current is {current}"
        )


async def ensure_user(conn: Connection, users: UserRepository, user_id: int) -> None:
    if not await users.exists(conn, user_id):
        raise UnauthorizedError(f"user {user_id} (X-User-Id) does not exist")


def jsonable(value: Any) -> Any:
    if isinstance(value, datetime | date):
        return value.isoformat()
    if isinstance(value, list):
        return [jsonable(v) for v in value]
    if isinstance(value, dict):
        return {k: jsonable(v) for k, v in value.items()}
    return value


def snapshot(values: Mapping[str, Any], fields: Iterable[str]) -> dict[str, Any]:
    return {f: jsonable(values.get(f)) for f in fields if values.get(f) not in (None, [], {})}


def changes_of(current: Mapping[str, Any], target: Mapping[str, Any], fields: Iterable[str]) -> dict[str, Any]:
    return {f: target.get(f) for f in fields if target.get(f) != current.get(f)}


def diff_of(current: Mapping[str, Any], changes: Mapping[str, Any]) -> dict[str, Any]:
    return {f: {"old": jsonable(current.get(f)), "new": jsonable(v)} for f, v in changes.items()}


def validate_model[M: BaseModel](model: type[M], data: dict[str, Any]) -> M:
    """Re-validate a merged (PATCH) document; errors become 422 problems."""
    try:
        return model.model_validate(data)
    except ValidationError as exc:
        errors = [
            {"loc": ["body", *exc_loc], "msg": e["msg"], "type": e["type"]}
            for e in exc.errors(include_url=False)
            for exc_loc in [list(e["loc"])]
        ]
        raise ValidationFailedError("merged document is invalid", errors=errors) from exc
