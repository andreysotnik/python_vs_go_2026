"""ISBN-10 / ISBN-13 validation, normalisation and generation helpers."""

from __future__ import annotations

import re

_STRIP = re.compile(r"[\s-]")


def normalize(raw: str) -> str:
    """Remove hyphens/spaces and upper-case a trailing x."""
    return _STRIP.sub("", raw).upper()


def isbn13_check_digit(first12: str) -> str:
    if len(first12) != 12 or not first12.isdigit():
        raise ValueError("ISBN-13 body must be 12 digits")
    total = sum(int(d) * (3 if i % 2 else 1) for i, d in enumerate(first12))
    return str((10 - total % 10) % 10)


def isbn10_check_digit(first9: str) -> str:
    if len(first9) != 9 or not first9.isdigit():
        raise ValueError("ISBN-10 body must be 9 digits")
    total = sum(int(d) * (10 - i) for i, d in enumerate(first9))
    check = (11 - total % 11) % 11
    return "X" if check == 10 else str(check)


def is_valid_isbn13(value: str) -> bool:
    return (
        len(value) == 13
        and value.isdigit()
        and value[:3] in ("978", "979")
        and isbn13_check_digit(value[:12]) == value[12]
    )


def is_valid_isbn10(value: str) -> bool:
    return (
        len(value) == 10
        and value[:9].isdigit()
        and (value[9].isdigit() or value[9] == "X")
        and isbn10_check_digit(value[:9]) == value[9]
    )


def isbn10_to_13(isbn10: str) -> str:
    body = "978" + isbn10[:9]
    return body + isbn13_check_digit(body)


def isbn13_to_10(isbn13: str) -> str | None:
    """Only 978-prefixed ISBN-13 have an ISBN-10 equivalent."""
    if not isbn13.startswith("978"):
        return None
    body = isbn13[3:12]
    return body + isbn10_check_digit(body)


def to_isbn13(raw: str) -> str:
    """Normalise any valid ISBN (10 or 13) into ISBN-13; raises ValueError if invalid."""
    value = normalize(raw)
    if is_valid_isbn13(value):
        return value
    if is_valid_isbn10(value):
        return isbn10_to_13(value)
    raise ValueError(f"invalid ISBN: {raw!r}")


def make_isbn13(prefix: str, number: int) -> str:
    """Build a valid ISBN-13 from a 978/979 prefix and a 9-digit number."""
    body = f"{prefix}{number:09d}"
    return body + isbn13_check_digit(body)
