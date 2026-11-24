"""Shared fixtures: a real PostgreSQL (testcontainers, PGDATA on tmpfs) + in-process ASGI client."""

from __future__ import annotations

import os
import uuid
from collections.abc import AsyncIterator, Iterator
from pathlib import Path
from typing import Any

import asyncpg
import httpx
import pytest
from alembic import command
from alembic.config import Config

from library.config import Settings
from library.main import create_app

ROOT = Path(__file__).resolve().parent.parent


def _migrate(url: str) -> None:
    cfg = Config(str(ROOT / "alembic.ini"))
    cfg.set_main_option("script_location", str(ROOT / "migrations"))
    cfg.set_main_option("sqlalchemy.url", url)
    command.upgrade(cfg, "head")


@pytest.fixture(scope="session")
def database_url() -> Iterator[str]:
    explicit = os.environ.get("TEST_DATABASE_URL")
    if explicit:
        yield explicit
        return
    # Docker Desktop (macOS): Ryuk must mount the VM-side socket path, not the host one.
    os.environ.setdefault("TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE", "/var/run/docker.sock")
    # Ryuk's port mapping is flaky on Docker Desktop; the context manager below removes the container.
    os.environ.setdefault("TESTCONTAINERS_RYUK_DISABLED", "true")
    from testcontainers.postgres import PostgresContainer  # noqa: PLC0415 - optional, only without TEST_DATABASE_URL

    container = PostgresContainer("postgres:16", username="test", password="test", dbname="test", driver=None)
    # tmpfs keeps the test database in RAM: fast, and independent of the Docker VM disk.
    container.with_tmpfs_mount("/var/lib/postgresql/data", size="size=1g")
    with container as pg:
        yield pg.get_connection_url()


@pytest.fixture(scope="session")
def migrated_url(database_url: str) -> str:
    _migrate(database_url)
    return database_url


@pytest.fixture(scope="session")
async def client(migrated_url: str) -> AsyncIterator[httpx.AsyncClient]:
    settings = Settings(database_url=migrated_url, db_pool_min=1, db_pool_max=5, search_max_candidates=1000)
    app = create_app(settings, manage_logging=False)
    async with app.router.lifespan_context(app):
        transport = httpx.ASGITransport(app=app, raise_app_exceptions=False)
        async with httpx.AsyncClient(transport=transport, base_url="http://test") as c:
            yield c


@pytest.fixture(scope="session")
async def db(migrated_url: str) -> AsyncIterator[asyncpg.Connection]:  # type: ignore[type-arg]
    conn = await asyncpg.connect(migrated_url)
    yield conn
    await conn.close()


async def make_user(client: httpx.AsyncClient, prefix: str = "user") -> str:
    name = f"{prefix}_{uuid.uuid4().hex[:10]}"
    resp = await client.post("/api/v1/users", json={"username": name, "email": f"{name}@example.org"})
    assert resp.status_code == 201, resp.text
    return str(resp.json()["id"])


@pytest.fixture(scope="session")
async def user_id(client: httpx.AsyncClient) -> str:
    return await make_user(client, "alice")


@pytest.fixture(scope="session")
async def other_user_id(client: httpx.AsyncClient) -> str:
    return await make_user(client, "bob")


def h(user: str, version: int | None = None) -> dict[str, str]:
    headers = {"X-User-Id": user}
    if version is not None:
        headers["If-Match"] = f'"{version}"'
    return headers


async def create_author(client: httpx.AsyncClient, user: str, **fields: Any) -> dict[str, Any]:
    body = {"full_name": f"Author {uuid.uuid4().hex[:6]}", **fields}
    resp = await client.post("/api/v1/authors", json=body, headers=h(user))
    assert resp.status_code == 201, resp.text
    return dict(resp.json())


async def create_book(client: httpx.AsyncClient, user: str, **fields: Any) -> dict[str, Any]:
    body = {"title": f"Book {uuid.uuid4().hex[:6]}", **fields}
    resp = await client.post("/api/v1/books", json=body, headers=h(user))
    assert resp.status_code == 201, resp.text
    return dict(resp.json())


async def walk(client: httpx.AsyncClient, url: str, params: dict[str, Any], limit: int) -> list[dict[str, Any]]:
    """Follow next_cursor until exhaustion, asserting page-size invariants."""
    items: list[dict[str, Any]] = []
    cursor = None
    for _ in range(1000):
        query = {**params, "limit": limit}
        if cursor:
            query["cursor"] = cursor
        resp = await client.get(url, params=query)
        assert resp.status_code == 200, resp.text
        page = resp.json()
        assert len(page["items"]) <= limit
        items.extend(page["items"])
        cursor = page["next_cursor"]
        if cursor is None:
            return items
        assert len(page["items"]) == limit
    raise AssertionError("pagination did not terminate")
