from datetime import date
from typing import Any

import pytest

from library.api.auth import AuthError, HeaderAuthenticator
from library.api.deps import if_match_version
from library.domain.cursor import decode_cursor
from library.domain.errors import BadRequestError, ConflictError, PreconditionRequiredError, ValidationFailedError
from library.domain.models import BookReplace
from library.services import common


def test_clamp_limit() -> None:
    assert common.clamp_limit(None, 20) == 20
    assert common.clamp_limit(500, 20) == 100
    assert common.clamp_limit(0, 20) == 1


def test_paginate_detects_next_page() -> None:
    rows: list[dict[str, Any]] = [{"id": i, "v": f"t{i}"} for i in range(1, 5)]
    items, cursor = common.paginate(rows, 3, lambda r: r["id"], "title", lambda r: r["v"])  # type: ignore[arg-type]
    assert items == [1, 2, 3]
    assert cursor is not None
    decoded = decode_cursor(cursor, "title", "text")
    assert decoded.value == "t3" and decoded.id == 3
    items, cursor = common.paginate(rows[:3], 3, lambda r: r["id"], "title", lambda r: r["v"])  # type: ignore[arg-type]
    assert cursor is None and items == [1, 2, 3]


def test_versions() -> None:
    assert common.resolve_version(3, 5) == 3
    assert common.resolve_version(None, 5) == 5
    assert common.resolve_version(None, None, required=False) is None
    with pytest.raises(PreconditionRequiredError):
        common.resolve_version(None, None)
    common.check_version(2, 2, "book", 1)
    common.check_version(2, None, "book", 1)
    with pytest.raises(ConflictError):
        common.check_version(2, 1, "book", 1)


def test_diffs() -> None:
    cur = {"title": "a", "year": 1, "genres": ["x"], "born": date(2000, 1, 1)}
    target = {"title": "b", "year": 1, "genres": ["x", "y"], "born": date(2001, 1, 1)}
    changes = common.changes_of(cur, target, cur.keys())
    assert set(changes) == {"title", "genres", "born"}
    diff = common.diff_of(cur, changes)
    assert diff["born"] == {"old": "2000-01-01", "new": "2001-01-01"}
    assert common.snapshot({"a": None, "b": [], "c": "x", "d": {}}, "abcd") == {"c": "x"}


def test_validate_model_maps_to_422() -> None:
    with pytest.raises(ValidationFailedError) as err:
        common.validate_model(BookReplace, {"title": ""})
    assert err.value.errors and err.value.errors[0]["loc"][0] == "body"


def test_header_authenticator() -> None:
    auth = HeaderAuthenticator()
    assert auth.authenticate({}) is None
    principal = auth.authenticate({"x-user-id": " 42 "})
    assert principal is not None and principal.user_id == 42
    for bad in ("abc", "-1", "0", "9" * 25):
        with pytest.raises(AuthError):
            auth.authenticate({"x-user-id": bad})


@pytest.mark.parametrize(("raw", "expected"), [(None, None), ('"3"', 3), ("3", 3), ('W/"12"', 12)])
def test_if_match(raw: str | None, expected: int | None) -> None:
    assert if_match_version(raw) == expected


def test_if_match_invalid() -> None:
    with pytest.raises(BadRequestError):
        if_match_version('"abc"')
