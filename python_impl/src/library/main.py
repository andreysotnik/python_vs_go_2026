"""Application factory."""

from __future__ import annotations

from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI

from library.api import authors, books, misc
from library.api.auth import AuthMiddleware, HeaderAuthenticator
from library.api.observability import ObservabilityMiddleware
from library.api.problems import install_exception_handlers
from library.config import Settings
from library.container import build_container
from library.db import Database, create_pool
from library.logging_setup import configure_logging

DESCRIPTION = """
World Library catalog API.

* **Pagination** is keyset-only: pass `next_cursor` back as `cursor`. No OFFSET.
* **Totals** are planner estimates (`include_total=true` -> `total_is_estimate: true`).
* **Ids** are 63-bit snowflake ids serialised as decimal strings.
* **Writes** require `X-User-Id`; updates require `If-Match: "<version>"` (or `version` in the body).
* **Errors** are RFC 7807 `application/problem+json`.
"""


def create_app(settings: Settings | None = None, *, manage_logging: bool = True) -> FastAPI:
    settings = settings or Settings()
    if manage_logging:
        configure_logging(settings.log_level)

    @asynccontextmanager
    async def lifespan(app: FastAPI) -> AsyncIterator[None]:
        pool = await create_pool(settings)
        try:
            app.state.container = build_container(Database(pool, settings.db_statement_timeout_ms), settings)
            yield
        finally:
            await pool.close()

    app = FastAPI(
        title="World Library API",
        version="1.0.0",
        description=DESCRIPTION,
        lifespan=lifespan,
        docs_url="/docs",
        redoc_url=None,
        openapi_url="/openapi.json",
    )
    install_exception_handlers(app)
    app.include_router(books.router)
    app.include_router(authors.router)
    app.include_router(misc.search_router)
    app.include_router(misc.users_router)
    app.include_router(misc.ops_router)
    app.add_middleware(AuthMiddleware, authenticator=HeaderAuthenticator())
    app.add_middleware(ObservabilityMiddleware)
    return app


def app_factory() -> FastAPI:
    """Entry point for `uvicorn --factory library.main:app_factory`."""
    return create_app()
