"""Snowflake-like ids (must match next_id() in migrations/sql/0001_initial.up.sql)."""

from __future__ import annotations

from datetime import UTC, datetime

EPOCH_MS = 1_577_836_800_000  # 2020-01-01T00:00:00Z
MAX_ID = (1 << 63) - 1


def make_id(ts_ms: int, node: int, seq: int) -> int:
    if not 0 <= node < 1024:
        raise ValueError("node must fit in 10 bits")
    return ((ts_ms - EPOCH_MS) << 22) | (node << 12) | (seq % 4096)


def id_timestamp(id_: int) -> datetime:
    return datetime.fromtimestamp(((id_ >> 22) + EPOCH_MS) / 1000, tz=UTC)
