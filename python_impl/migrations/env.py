"""Alembic environment: raw-SQL migrations, URL from DATABASE_URL (12-factor)."""

from __future__ import annotations

import os

from alembic import context
from sqlalchemy import create_engine, pool


def _sync_url() -> str:
    url = context.config.get_main_option("sqlalchemy.url") or os.environ["DATABASE_URL"]
    for prefix in ("postgresql://", "postgres://"):
        if url.startswith(prefix):
            return "postgresql+psycopg://" + url[len(prefix) :]
    return url


def run_migrations_online() -> None:
    engine = create_engine(_sync_url(), poolclass=pool.NullPool)
    with engine.connect() as connection:
        context.configure(connection=connection, transaction_per_migration=True)
        with context.begin_transaction():
            context.run_migrations()


if context.is_offline_mode():
    raise SystemExit("offline mode is not supported")
run_migrations_online()
