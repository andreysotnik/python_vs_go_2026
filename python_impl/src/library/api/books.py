"""/api/v1/books handlers (thin: parse -> service -> serialise)."""

from __future__ import annotations

from datetime import datetime
from typing import Annotated

from fastapi import APIRouter, Depends, Query, Response
from fastapi import status as http

from library.api.auth import require_user
from library.api.deps import CursorQ, IdPath, IfMatch, IncludeTotal, Limit, Services, etag
from library.api.problems import problem_responses
from library.domain.errors import ValidationFailedError
from library.domain.filters import BookFilters
from library.domain.models import Book, BookCard, BookCreate, BookPatch, BookReplace, HistoryEntry, Page
from library.services.books import normalize_isbn_filter

router = APIRouter(prefix="/api/v1/books", tags=["books"])
UserId = Annotated[int, Depends(require_user)]


def book_filters(
    title: Annotated[
        str | None, Query(min_length=3, max_length=200, description="substring/fuzzy title match (trigram)")
    ] = None,
    author: Annotated[
        str | None, Query(min_length=2, max_length=200, description="author name (full-text, prefix on last word)")
    ] = None,
    author_id: Annotated[int | None, Query(gt=0, description="books of this author")] = None,
    isbn: Annotated[str | None, Query(max_length=32, description="ISBN-10 or ISBN-13, hyphens allowed")] = None,
    language: Annotated[str | None, Query(pattern=r"^[A-Za-z]{2,3}$", description="ISO 639 code")] = None,
    year_from: Annotated[int | None, Query(ge=-3000, le=2100)] = None,
    year_to: Annotated[int | None, Query(ge=-3000, le=2100)] = None,
    publisher: Annotated[str | None, Query(min_length=1, max_length=256, description="exact publisher name")] = None,
    genre: Annotated[str | None, Query(min_length=1, max_length=64)] = None,
    created_by: Annotated[int | None, Query(gt=0, description="user id who created the book")] = None,
    created_from: Annotated[datetime | None, Query(description="created_at >= (RFC 3339)")] = None,
    created_to: Annotated[datetime | None, Query(description="created_at <= (RFC 3339)")] = None,
    q: Annotated[
        str | None, Query(min_length=1, max_length=200, description="full-text query (websearch syntax)")
    ] = None,
) -> BookFilters:
    if year_from is not None and year_to is not None and year_from > year_to:
        raise ValidationFailedError(
            "year_from must be <= year_to",
            errors=[{"loc": ["query", "year_from"], "msg": "year_from > year_to", "type": "range"}],
        )
    if created_from is not None and created_to is not None and created_from > created_to:
        raise ValidationFailedError(
            "created_from must be <= created_to",
            errors=[{"loc": ["query", "created_from"], "msg": "created_from > created_to", "type": "range"}],
        )
    return BookFilters(
        title=title,
        author=author,
        author_id=author_id,
        isbn13=normalize_isbn_filter(isbn),
        language=language.lower() if language else None,
        year_from=year_from,
        year_to=year_to,
        publisher=publisher,
        genre=genre.lower() if genre else None,
        created_by=created_by,
        created_from=created_from,
        created_to=created_to,
        q=q,
    )


@router.get(
    "",
    response_model=Page[Book],
    summary="List/search books (keyset pagination)",
    responses=problem_responses(400, 422, 503),
)
async def list_books(
    svc: Services,
    filters: Annotated[BookFilters, Depends(book_filters)],
    sort: Annotated[
        str | None,
        Query(
            pattern=r"^[+-]?(title|publication_year|created_at)$",
            description="title | publication_year | created_at, prefix '-' for descending (default -created_at)",
        ),
    ] = None,
    limit: Limit = None,
    cursor: CursorQ = None,
    include_total: IncludeTotal = False,
) -> Page[Book]:
    return await svc.books.find(filters, sort, cursor, limit, include_total)


@router.get(
    "/{book_id}",
    response_model=BookCard,
    summary="Book card with authors and audit fields",
    responses=problem_responses(404, 422),
)
async def get_book(svc: Services, book_id: IdPath, response: Response) -> BookCard:
    card = await svc.books.get(book_id)
    response.headers["ETag"] = etag(card.version)
    return card


@router.post(
    "",
    response_model=BookCard,
    status_code=http.HTTP_201_CREATED,
    summary="Create a book",
    responses=problem_responses(400, 401, 409, 422),
)
async def create_book(svc: Services, body: BookCreate, user_id: UserId, response: Response) -> BookCard:
    card = await svc.books.create(body, user_id)
    response.headers["ETag"] = etag(card.version)
    response.headers["Location"] = f"/api/v1/books/{card.id}"
    return card


@router.put(
    "/{book_id}",
    response_model=BookCard,
    summary="Replace a book (optimistic locking)",
    responses=problem_responses(400, 401, 404, 409, 422, 428),
)
async def replace_book(
    svc: Services, book_id: IdPath, body: BookReplace, user_id: UserId, if_match: IfMatch, response: Response
) -> BookCard:
    card = await svc.books.replace(book_id, body, if_match, user_id)
    response.headers["ETag"] = etag(card.version)
    return card


@router.patch(
    "/{book_id}",
    response_model=BookCard,
    summary="Partially update a book (JSON merge patch)",
    responses=problem_responses(400, 401, 404, 409, 422, 428),
)
async def patch_book(
    svc: Services, book_id: IdPath, body: BookPatch, user_id: UserId, if_match: IfMatch, response: Response
) -> BookCard:
    card = await svc.books.patch(book_id, body, if_match, user_id)
    response.headers["ETag"] = etag(card.version)
    return card


@router.delete(
    "/{book_id}",
    status_code=http.HTTP_204_NO_CONTENT,
    summary="Soft-delete a book",
    responses=problem_responses(400, 401, 404, 409),
)
async def delete_book(svc: Services, book_id: IdPath, user_id: UserId, if_match: IfMatch) -> Response:
    await svc.books.delete(book_id, if_match, user_id)
    return Response(status_code=http.HTTP_204_NO_CONTENT)


@router.get(
    "/{book_id}/history",
    response_model=Page[HistoryEntry],
    summary="Change history (audit log)",
    responses=problem_responses(400, 404, 422),
)
async def book_history(
    svc: Services, book_id: IdPath, limit: Limit = None, cursor: CursorQ = None
) -> Page[HistoryEntry]:
    return await svc.books.history(book_id, cursor, limit)
