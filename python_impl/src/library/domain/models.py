"""Pydantic v2 schemas for API input/output (also the source of the OpenAPI document)."""

from __future__ import annotations

import re
from datetime import date, datetime
from typing import Annotated, Any, Literal

from pydantic import (
    BaseModel,
    BeforeValidator,
    ConfigDict,
    Field,
    PlainSerializer,
    WithJsonSchema,
    field_validator,
    model_validator,
)

from library.domain import isbn as isbn_lib
from library.domain.ids import MAX_ID


def _parse_id(value: Any) -> Any:
    if isinstance(value, str):
        if not value.isdigit():
            raise ValueError("id must be a decimal string")
        return int(value)
    return value


# 63-bit snowflake ids exceed JS Number precision, so they travel as decimal strings.
ID = Annotated[
    int,
    BeforeValidator(_parse_id),
    Field(gt=0, le=MAX_ID),
    PlainSerializer(str, return_type=str, when_used="json"),
    WithJsonSchema({"type": "string", "pattern": "^[0-9]{1,19}$", "examples": ["7395413523316707328"]}),
]

Role = Literal["author", "co-author", "translator", "editor", "illustrator"]
ROLES: tuple[str, ...] = ("author", "co-author", "translator", "editor", "illustrator")

_LANG = re.compile(r"^[a-z]{2,3}$")
_COUNTRY = re.compile(r"^[A-Z]{2}$")
_EXT_KEY = re.compile(r"^[a-z0-9_]{1,32}$")
_GENRE = re.compile(r"^[\w][\w &'-]{0,63}$")

MIN_YEAR, MAX_YEAR = -3000, 2100


class StrictModel(BaseModel):
    model_config = ConfigDict(extra="forbid", str_strip_whitespace=True)


def _check_external_ids(value: dict[str, str] | None) -> dict[str, str] | None:
    if value is None:
        return None
    if len(value) > 32:
        raise ValueError("at most 32 external ids")
    for key, val in value.items():
        if not _EXT_KEY.match(key):
            raise ValueError(f"external id key {key!r} must match ^[a-z0-9_]{{1,32}}$")
        if not 1 <= len(val) <= 256:
            raise ValueError(f"external id {key!r} must be 1..256 chars")
    return value


def _check_language(value: str | None) -> str | None:
    if value is None:
        return None
    value = value.lower()
    if not _LANG.match(value):
        raise ValueError("language_code must be an ISO 639-1/639-3 code (2-3 latin letters)")
    return value


def _check_country(value: str | None) -> str | None:
    if value is None:
        return None
    value = value.upper()
    if not _COUNTRY.match(value):
        raise ValueError("country_code must be an ISO 3166-1 alpha-2 code")
    return value


def _check_genres(value: list[str] | None) -> list[str] | None:
    if value is None:
        return None
    seen: list[str] = []
    for genre in value:
        g = genre.strip().lower()
        if not _GENRE.match(g):
            raise ValueError(f"invalid genre {genre!r}")
        if g not in seen:
            seen.append(g)
    if len(seen) > 16:
        raise ValueError("at most 16 genres")
    return seen


# ---------------------------------------------------------------- users


class UserCreate(StrictModel):
    username: str = Field(pattern=r"^[A-Za-z0-9_.-]{3,64}$", examples=["librarian_anna"])
    email: str = Field(max_length=254, pattern=r"^[^@\s]+@[^@\s]+$", examples=["anna@library.org"])


class User(BaseModel):
    id: ID
    username: str
    email: str
    created_at: datetime


# ---------------------------------------------------------------- authors


class AuthorFields(StrictModel):
    full_name: str = Field(min_length=1, max_length=512, examples=["Lev Tolstoy"])
    original_name: str | None = Field(default=None, min_length=1, max_length=512, examples=["Лев Толстой"])
    birth_date: date | None = Field(default=None, examples=["1828-09-09"])
    death_date: date | None = Field(default=None, examples=["1910-11-20"])
    country_code: str | None = Field(default=None, examples=["RU"], description="ISO 3166-1 alpha-2")
    biography: str | None = Field(default=None, max_length=20000)
    external_ids: dict[str, str] | None = Field(default=None, examples=[{"viaf": "96987389", "wikidata": "Q7243"}])

    _country = field_validator("country_code")(_check_country)
    _ext = field_validator("external_ids")(_check_external_ids)

    @field_validator("birth_date", "death_date")
    @classmethod
    def _date_range(cls, value: date | None) -> date | None:
        if value is not None and value.year > MAX_YEAR:
            raise ValueError(f"date must not be after year {MAX_YEAR}")
        return value

    @model_validator(mode="after")
    def _lifespan(self) -> AuthorFields:
        if self.birth_date and self.death_date and self.death_date < self.birth_date:
            raise ValueError("death_date must not be earlier than birth_date")
        return self


class AuthorCreate(AuthorFields):
    pass


class AuthorReplace(AuthorFields):
    """PUT body: full replacement; `version` may be given instead of If-Match."""

    version: int | None = Field(default=None, ge=1)


class AuthorPatch(StrictModel):
    """PATCH body (JSON merge-patch semantics): absent = unchanged, null = cleared."""

    full_name: str | None = Field(default=None, min_length=1, max_length=512)
    original_name: str | None = Field(default=None, min_length=1, max_length=512)
    birth_date: date | None = None
    death_date: date | None = None
    country_code: str | None = None
    biography: str | None = Field(default=None, max_length=20000)
    external_ids: dict[str, str] | None = None
    version: int | None = Field(default=None, ge=1)

    _country = field_validator("country_code")(_check_country)
    _ext = field_validator("external_ids")(_check_external_ids)

    @field_validator("full_name")
    @classmethod
    def _not_null_name(cls, value: str | None) -> str:
        if value is None:
            raise ValueError("full_name cannot be null")
        return value


class AuditFields(BaseModel):
    created_at: datetime
    created_by: ID
    updated_at: datetime
    updated_by: ID
    version: int


class _AuthorCore(BaseModel):
    id: ID
    full_name: str
    original_name: str | None
    birth_date: date | None
    death_date: date | None
    country_code: str | None
    biography: str | None
    external_ids: dict[str, str]


class Author(AuditFields, _AuthorCore):
    """Field order: entity fields first, audit fields last (MRO-driven in pydantic)."""


class AuthorCard(Author):
    created_by_username: str | None = None
    updated_by_username: str | None = None


# ---------------------------------------------------------------- books


class BookAuthorLink(StrictModel):
    author_id: ID
    role: Role = "author"
    ordinal: int = Field(default=1, ge=1, le=1000)


def _check_links(value: list[BookAuthorLink] | None) -> list[BookAuthorLink] | None:
    if value is None:
        return None
    if len(value) > 100:
        raise ValueError("at most 100 author links")
    keys = {(link.author_id, link.role) for link in value}
    if len(keys) != len(value):
        raise ValueError("duplicate (author_id, role) pair")
    return value


class BookFields(StrictModel):
    title: str = Field(min_length=1, max_length=1024, examples=["Война и мир"])
    original_title: str | None = Field(default=None, min_length=1, max_length=1024)
    subtitle: str | None = Field(default=None, max_length=1024)
    description: str | None = Field(default=None, max_length=20000)
    isbn_10: str | None = Field(default=None, examples=["0140447938"])
    isbn_13: str | None = Field(default=None, examples=["9780140447934"])
    language_code: str | None = Field(default=None, examples=["ru"], description="ISO 639-1/639-3")
    publication_year: int | None = Field(default=None, ge=MIN_YEAR, le=MAX_YEAR, examples=[1869])
    publisher: str | None = Field(default=None, min_length=1, max_length=256, examples=["Penguin Classics"])
    page_count: int | None = Field(default=None, ge=1, le=100000)
    genres: list[str] | None = Field(default=None, examples=[["novel", "historical"]])
    edition: str | None = Field(default=None, max_length=128)
    external_ids: dict[str, str] | None = Field(default=None, examples=[{"openlibrary": "OL267096W"}])
    authors: list[BookAuthorLink] | None = None

    _lang = field_validator("language_code")(_check_language)
    _genres = field_validator("genres")(_check_genres)
    _ext = field_validator("external_ids")(_check_external_ids)
    _links = field_validator("authors")(_check_links)

    @field_validator("isbn_10")
    @classmethod
    def _isbn10(cls, value: str | None) -> str | None:
        if value is None:
            return None
        norm = isbn_lib.normalize(value)
        if not isbn_lib.is_valid_isbn10(norm):
            raise ValueError("invalid ISBN-10 (format or check digit)")
        return norm

    @field_validator("isbn_13")
    @classmethod
    def _isbn13(cls, value: str | None) -> str | None:
        if value is None:
            return None
        norm = isbn_lib.normalize(value)
        if not isbn_lib.is_valid_isbn13(norm):
            raise ValueError("invalid ISBN-13 (format or check digit)")
        return norm

    @model_validator(mode="after")
    def _isbn_consistency(self) -> BookFields:
        reconcile_isbns(self)
        return self


def reconcile_isbns(obj: Any) -> None:
    """Derive the missing ISBN form and verify that both forms denote the same book."""
    i10, i13 = obj.isbn_10, obj.isbn_13
    if i10 and not i13:
        obj.isbn_13 = isbn_lib.isbn10_to_13(i10)
    elif i13 and not i10:
        obj.isbn_10 = isbn_lib.isbn13_to_10(i13)
    elif i10 and i13 and isbn_lib.isbn10_to_13(i10) != i13:
        raise ValueError("isbn_10 and isbn_13 denote different books")


class BookCreate(BookFields):
    pass


class BookReplace(BookFields):
    version: int | None = Field(default=None, ge=1)


class BookPatch(StrictModel):
    title: str | None = Field(default=None, min_length=1, max_length=1024)
    original_title: str | None = Field(default=None, min_length=1, max_length=1024)
    subtitle: str | None = Field(default=None, max_length=1024)
    description: str | None = Field(default=None, max_length=20000)
    isbn_10: str | None = None
    isbn_13: str | None = None
    language_code: str | None = None
    publication_year: int | None = Field(default=None, ge=MIN_YEAR, le=MAX_YEAR)
    publisher: str | None = Field(default=None, min_length=1, max_length=256)
    page_count: int | None = Field(default=None, ge=1, le=100000)
    genres: list[str] | None = None
    edition: str | None = Field(default=None, max_length=128)
    external_ids: dict[str, str] | None = None
    authors: list[BookAuthorLink] | None = None
    version: int | None = Field(default=None, ge=1)

    _lang = field_validator("language_code")(_check_language)
    _genres = field_validator("genres")(_check_genres)
    _ext = field_validator("external_ids")(_check_external_ids)
    _links = field_validator("authors")(_check_links)
    _isbn10 = field_validator("isbn_10")(BookFields._isbn10.__func__)  # type: ignore[attr-defined]
    _isbn13 = field_validator("isbn_13")(BookFields._isbn13.__func__)  # type: ignore[attr-defined]

    @field_validator("title")
    @classmethod
    def _not_null_title(cls, value: str | None) -> str:
        if value is None:
            raise ValueError("title cannot be null")
        return value


class BookAuthor(BaseModel):
    id: ID
    full_name: str
    role: Role
    ordinal: int


class _BookCore(BaseModel):
    id: ID
    title: str
    original_title: str | None
    subtitle: str | None
    description: str | None
    isbn_10: str | None
    isbn_13: str | None
    language_code: str | None
    publication_year: int | None
    publisher: str | None
    page_count: int | None
    genres: list[str]
    edition: str | None
    external_ids: dict[str, str]
    authors: list[BookAuthor] = Field(default_factory=list)


class Book(AuditFields, _BookCore):
    """Field order: entity fields first, audit fields last (MRO-driven in pydantic)."""


class BookCard(Book):
    created_by_username: str | None = None
    updated_by_username: str | None = None


class AuthorBook(Book):
    roles: list[Role]


# ---------------------------------------------------------------- history / search / lists


class HistoryEntry(BaseModel):
    id: ID
    entity_type: Literal["book", "author"]
    entity_id: ID
    action: Literal["create", "update", "delete"]
    changed_by: ID
    changed_by_username: str | None
    changed_at: datetime
    diff: dict[str, Any] | None = Field(
        description="create/delete: snapshot of fields; update: {field: {old, new}}",
        examples=[{"title": {"old": "Old", "new": "New"}}],
    )


class SearchHit(BaseModel):
    type: Literal["book", "author"]
    id: ID
    title: str = Field(description="book title or author full name")
    subtitle: str | None = Field(description="book original title / author original name")
    score: float = Field(description="ts_rank + word_similarity")


class Page[T](BaseModel):
    items: list[T]
    next_cursor: str | None = Field(description="opaque keyset cursor; null on the last page")
    limit: int
    total: int | None = Field(description="only with include_total=true; an estimate, never COUNT(*)")
    total_is_estimate: bool


class Problem(BaseModel):
    """RFC 7807 problem details."""

    type: str = Field(examples=["https://library.example/problems/not-found"])
    title: str = Field(examples=["Not Found"])
    status: int = Field(examples=[404])
    detail: str | None = Field(default=None, examples=["book 42 not found"])
    instance: str | None = Field(default=None, examples=["/api/v1/books/42"])
    errors: list[dict[str, Any]] | None = Field(
        default=None, examples=[[{"loc": ["body", "title"], "msg": "Field required", "type": "missing"}]]
    )


class Health(BaseModel):
    status: Literal["ok", "unavailable"]
    detail: str | None = None
