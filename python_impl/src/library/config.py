"""12-factor configuration: everything comes from environment variables."""

from __future__ import annotations

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="", case_sensitive=False, extra="ignore")

    database_url: str = Field(default="postgresql://library:library@localhost:15432/library")
    db_pool_min: int = Field(default=2, ge=0)
    db_pool_max: int = Field(default=10, ge=1)
    # Per-transaction statement timeout (SET LOCAL => safe behind PgBouncer transaction mode).
    db_statement_timeout_ms: int = Field(default=5000, ge=100)
    # asyncpg prepared statement cache. Set to 0 behind PgBouncer < 1.21 in transaction mode.
    db_statement_cache_size: int = Field(default=256, ge=0)
    db_command_timeout_s: float = Field(default=10.0, gt=0)

    log_level: str = Field(default="INFO")
    # Search ranks at most this many FTS/trigram candidates per entity type (bounded cost).
    search_max_candidates: int = Field(default=1000, ge=10, le=100_000)
    default_page_size: int = Field(default=20, ge=1, le=100)
