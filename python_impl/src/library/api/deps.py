"""FastAPI dependencies: services from app.state, common query/header parsing."""

from __future__ import annotations

import re
from typing import Annotated

from fastapi import Depends, Header, Path, Query, Request

from library.container import Container
from library.domain.errors import BadRequestError

_IF_MATCH = re.compile(r'^\s*(?:W/)?"?(\d{1,9})"?\s*$')


def container(request: Request) -> Container:
    c: Container = request.app.state.container
    return c


Services = Annotated[Container, Depends(container)]

IdPath = Annotated[int, Path(gt=0, le=(1 << 63) - 1, description="snowflake id (decimal)")]

Limit = Annotated[int | None, Query(ge=1, le=100, description="page size, 1..100 (default 20)")]
CursorQ = Annotated[
    str | None,
    Query(max_length=2048, description="opaque cursor from `next_cursor` of the previous page"),
]
IncludeTotal = Annotated[bool, Query(description="return an approximate total (planner estimate, never COUNT(*))")]


def if_match_version(
    if_match: Annotated[
        str | None,
        Header(
            alias="If-Match",
            description='current version of the resource, e.g. "3" (as returned in ETag)',
        ),
    ] = None,
) -> int | None:
    if if_match is None:
        return None
    m = _IF_MATCH.match(if_match)
    if not m:
        raise BadRequestError('If-Match must be an entity tag with the numeric version, e.g. "3"')
    return int(m.group(1))


IfMatch = Annotated[int | None, Depends(if_match_version)]


def etag(version: int) -> str:
    return f'"{version}"'
