"""Opaque keyset cursors: base64url(JSON{sort key, last sort value, last id})."""

from __future__ import annotations

import base64
import binascii
import json
from dataclasses import dataclass
from datetime import date, datetime
from typing import Any

from library.domain.errors import BadRequestError
from library.domain.sorting import ValueKind

CursorValue = str | int | float | datetime | date


@dataclass(frozen=True, slots=True)
class Cursor:
    sort: str
    value: CursorValue
    id: int


def _encode_value(value: CursorValue) -> Any:
    if isinstance(value, datetime | date):
        return value.isoformat()
    return value


def _decode_value(raw: Any, kind: ValueKind) -> CursorValue:
    if kind == "text" and isinstance(raw, str):
        return raw
    if kind == "int" and isinstance(raw, int) and not isinstance(raw, bool):
        return raw
    if kind == "float" and isinstance(raw, int | float) and not isinstance(raw, bool):
        return float(raw)
    if kind == "timestamp" and isinstance(raw, str):
        parsed = datetime.fromisoformat(raw)
        if parsed.tzinfo is None:
            raise ValueError("timestamp without timezone")
        return parsed
    if kind == "date" and isinstance(raw, str):
        if raw == "infinity":
            return date.max
        return date.fromisoformat(raw)
    raise ValueError(f"cursor value does not match kind {kind}")


def encode_cursor(sort: str, value: CursorValue, id_: int) -> str:
    if isinstance(value, date) and not isinstance(value, datetime) and value == date.max:
        encoded: Any = "infinity"
    else:
        encoded = _encode_value(value)
    payload = json.dumps({"s": sort, "v": encoded, "i": id_}, separators=(",", ":"), ensure_ascii=False)
    return base64.urlsafe_b64encode(payload.encode()).decode().rstrip("=")


def decode_cursor(token: str, expected_sort: str, kind: ValueKind) -> Cursor:
    """Decode and validate a cursor; any malformation -> 400."""
    try:
        padded = token + "=" * (-len(token) % 4)
        payload = json.loads(base64.urlsafe_b64decode(padded.encode()).decode())
        if not isinstance(payload, dict):
            raise ValueError("cursor payload is not an object")
        sort, raw_value, id_ = payload["s"], payload["v"], payload["i"]
        if not isinstance(id_, int) or isinstance(id_, bool) or id_ <= 0:
            raise ValueError("bad id")
        value = _decode_value(raw_value, kind)
    except (ValueError, KeyError, TypeError, binascii.Error, UnicodeDecodeError) as exc:
        raise BadRequestError("malformed cursor") from exc
    if sort != expected_sort:
        raise BadRequestError(
            f"cursor was issued for sort {sort!r}, but request uses {expected_sort!r}; "
            "restart pagination without cursor"
        )
    return Cursor(sort=sort, value=value, id=id_)
