from datetime import UTC, date, datetime

import pytest

from library.domain.cursor import decode_cursor, encode_cursor
from library.domain.errors import BadRequestError
from library.domain.sorting import AUTHOR_SORTS, BOOK_SORTS, parse_sort


@pytest.mark.parametrize(
    ("kind", "value"),
    [
        ("text", "Война и мир"),
        ("text", ""),
        ("int", 1869),
        ("int", 32767),
        ("float", 0.123456789012345),
        ("timestamp", datetime(2024, 5, 1, 12, 30, 0, 123456, tzinfo=UTC)),
        ("date", date(1828, 9, 9)),
        ("date", date.max),
    ],
)
def test_cursor_roundtrip(kind: str, value: object) -> None:
    token = encode_cursor("-x", value, 42)  # type: ignore[arg-type]
    assert "=" not in token
    cur = decode_cursor(token, "-x", kind)  # type: ignore[arg-type]
    assert cur.value == value
    assert cur.id == 42
    assert cur.sort == "-x"


@pytest.mark.parametrize(
    "token",
    [
        "!!!",
        "bm90IGpzb24",  # "not json"
        encode_cursor("title", "a", 1)[:-3],
        "WzEsMiwzXQ",  # [1,2,3]
    ],
)
def test_cursor_malformed(token: str) -> None:
    with pytest.raises(BadRequestError):
        decode_cursor(token, "title", "text")


def test_cursor_type_mismatch_and_bad_id() -> None:
    with pytest.raises(BadRequestError):
        decode_cursor(encode_cursor("title", 5, 1), "title", "text")
    with pytest.raises(BadRequestError):
        decode_cursor(encode_cursor("title", "a", 0), "title", "text")
    with pytest.raises(BadRequestError):
        decode_cursor(encode_cursor("created_at", "2024-01-01T00:00:00", 1), "created_at", "timestamp")


def test_cursor_sort_mismatch() -> None:
    token = encode_cursor("title", "a", 1)
    with pytest.raises(BadRequestError, match="restart pagination"):
        decode_cursor(token, "-title", "text")


def test_parse_sort() -> None:
    s = parse_sort(None, BOOK_SORTS, "-created_at")
    assert s.descending and s.field.name == "created_at" and s.key == "-created_at"
    s = parse_sort("+title", BOOK_SORTS, "-created_at")
    assert not s.descending and s.key == "title"
    s = parse_sort("-birth_date", AUTHOR_SORTS, "full_name")
    assert "infinity" in s.field.expr
    with pytest.raises(BadRequestError):
        parse_sort("isbn_13", BOOK_SORTS, "title")
