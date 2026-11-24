"""Validated list filters (parsed by the API layer, consumed by repositories)."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import date, datetime


@dataclass(frozen=True, slots=True)
class BookFilters:
    title: str | None = None
    author: str | None = None
    author_id: int | None = None
    isbn13: str | None = None
    language: str | None = None
    year_from: int | None = None
    year_to: int | None = None
    publisher: str | None = None
    genre: str | None = None
    created_by: int | None = None
    created_from: datetime | None = None
    created_to: datetime | None = None
    q: str | None = None


@dataclass(frozen=True, slots=True)
class AuthorFilters:
    name: str | None = None
    country: str | None = None
    born_from: date | None = None
    born_to: date | None = None
    q: str | None = None
