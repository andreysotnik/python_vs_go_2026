"""User use-cases (minimal: who performs changes)."""

from __future__ import annotations

from library.db import Database
from library.domain.errors import ConflictError, NotFoundError
from library.domain.models import User, UserCreate
from library.repositories.users import UserRepository


class UserService:
    def __init__(self, db: Database, users: UserRepository) -> None:
        self.db = db
        self.users = users

    async def create(self, data: UserCreate) -> User:
        async with self.db.tx() as conn:
            row = await self.users.insert(conn, data.username, data.email)
        if row is None:
            raise ConflictError("username or email already taken")
        return User.model_validate(dict(row))

    async def get(self, user_id: int) -> User:
        async with self.db.tx(readonly=True) as conn:
            row = await self.users.get(conn, user_id)
        if row is None:
            raise NotFoundError(f"user {user_id} not found")
        return User.model_validate(dict(row))
