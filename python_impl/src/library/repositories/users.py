"""Users persistence."""

from __future__ import annotations

import asyncpg

from library.db import Connection


class UserRepository:
    async def insert(self, conn: Connection, username: str, email: str) -> asyncpg.Record | None:
        """Returns None when username/email is already taken."""
        return await conn.fetchrow(
            "INSERT INTO users (username, email) VALUES ($1, $2) ON CONFLICT DO NOTHING "
            "RETURNING id, username, email, created_at",
            username,
            email,
        )

    async def get(self, conn: Connection, user_id: int) -> asyncpg.Record | None:
        return await conn.fetchrow("SELECT id, username, email, created_at FROM users WHERE id = $1", user_id)

    async def exists(self, conn: Connection, user_id: int) -> bool:
        return bool(await conn.fetchval("SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)", user_id))
