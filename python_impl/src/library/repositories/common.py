"""Helpers shared by repositories."""

from __future__ import annotations

from typing import Any

from library.db import Connection


async def explain_rows(conn: Connection, sql: str, params: list[Any]) -> int:
    """Planner row estimate for a query (used for `total` instead of COUNT(*))."""
    plan = await conn.fetchval(f"EXPLAIN (FORMAT JSON) {sql}", *params)
    return int(plan[0]["Plan"]["Plan Rows"])
