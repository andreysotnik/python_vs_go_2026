"""Append-only audit log (range-partitioned by month)."""

from __future__ import annotations

from datetime import datetime
from typing import Any

import asyncpg

from library.db import Connection


class AuditRepository:
    async def record(
        self,
        conn: Connection,
        entity_type: str,
        entity_id: int,
        action: str,
        changed_by: int,
        diff: dict[str, Any] | None,
    ) -> None:
        await conn.execute(
            "INSERT INTO audit_log (entity_type, entity_id, action, changed_by, diff) VALUES ($1, $2, $3, $4, $5)",
            entity_type,
            entity_id,
            action,
            changed_by,
            diff,
        )

    async def history(
        self,
        conn: Connection,
        entity_type: str,
        entity_id: int,
        after: tuple[datetime, int] | None,
        limit: int,
    ) -> list[asyncpg.Record]:
        params: list[Any] = [entity_type, entity_id]
        keyset = ""
        if after is not None:
            params += [after[0], after[1]]
            keyset = " AND (al.changed_at, al.id) > ($3::timestamptz, $4::bigint)"
        return await conn.fetch(
            "SELECT al.id, al.entity_type, al.entity_id, al.action, al.changed_by, "
            "u.username AS changed_by_username, al.changed_at, al.diff "
            "FROM audit_log al LEFT JOIN users u ON u.id = al.changed_by "
            f"WHERE al.entity_type = $1 AND al.entity_id = $2{keyset} "
            f"ORDER BY al.changed_at, al.id LIMIT {int(limit) + 1}",
            *params,
        )
