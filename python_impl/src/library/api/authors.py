"""/api/v1/authors handlers."""

from __future__ import annotations

from datetime import date
from typing import Annotated

from fastapi import APIRouter, Depends, Query, Response
from fastapi import status as http

from library.api.auth import require_user
from library.api.deps import CursorQ, IdPath, IfMatch, IncludeTotal, Limit, Services, etag
from library.api.problems import problem_responses
from library.domain.errors import ValidationFailedError
from library.domain.filters import AuthorFilters
from library.domain.models import (
    Author,
    AuthorBook,
    AuthorCard,
    AuthorCreate,
    AuthorPatch,
    AuthorReplace,
    HistoryEntry,
    Page,
)

router = APIRouter(prefix="/api/v1/authors", tags=["authors"])
UserId = Annotated[int, Depends(require_user)]


def author_filters(
    name: Annotated[
        str | None, Query(min_length=3, max_length=200, description="substring/fuzzy name match (trigram)")
    ] = None,
    country: Annotated[str | None, Query(pattern=r"^[A-Za-z]{2}$", description="ISO 3166-1 alpha-2")] = None,
    born_from: Annotated[date | None, Query(description="birth_date >=")] = None,
    born_to: Annotated[date | None, Query(description="birth_date <=")] = None,
    q: Annotated[str | None, Query(min_length=1, max_length=200, description="full-text query")] = None,
) -> AuthorFilters:
    if born_from and born_to and born_from > born_to:
        raise ValidationFailedError(
            "born_from must be <= born_to",
            errors=[{"loc": ["query", "born_from"], "msg": "born_from > born_to", "type": "range"}],
        )
    return AuthorFilters(
        name=name, country=country.upper() if country else None, born_from=born_from, born_to=born_to, q=q
    )


@router.get(
    "",
    response_model=Page[Author],
    summary="List/search authors (keyset pagination)",
    responses=problem_responses(400, 422, 503),
)
async def list_authors(
    svc: Services,
    filters: Annotated[AuthorFilters, Depends(author_filters)],
    sort: Annotated[
        str | None,
        Query(
            pattern=r"^[+-]?(full_name|birth_date|created_at)$",
            description="full_name | birth_date | created_at, prefix '-' for descending (default full_name)",
        ),
    ] = None,
    limit: Limit = None,
    cursor: CursorQ = None,
    include_total: IncludeTotal = False,
) -> Page[Author]:
    return await svc.authors.find(filters, sort, cursor, limit, include_total)


@router.get(
    "/{author_id}",
    response_model=AuthorCard,
    summary="Author card with audit fields",
    responses=problem_responses(404, 422),
)
async def get_author(svc: Services, author_id: IdPath, response: Response) -> AuthorCard:
    card = await svc.authors.get(author_id)
    response.headers["ETag"] = etag(card.version)
    return card


@router.get(
    "/{author_id}/books",
    response_model=Page[AuthorBook],
    summary="Books of an author (newest first)",
    responses=problem_responses(400, 404, 422),
)
async def author_books(
    svc: Services, author_id: IdPath, limit: Limit = None, cursor: CursorQ = None
) -> Page[AuthorBook]:
    return await svc.authors.books_of(author_id, cursor, limit)


@router.get(
    "/{author_id}/history",
    response_model=Page[HistoryEntry],
    summary="Change history (audit log)",
    responses=problem_responses(400, 404, 422),
)
async def author_history(
    svc: Services, author_id: IdPath, limit: Limit = None, cursor: CursorQ = None
) -> Page[HistoryEntry]:
    return await svc.authors.history(author_id, cursor, limit)


@router.post(
    "",
    response_model=AuthorCard,
    status_code=http.HTTP_201_CREATED,
    summary="Create an author",
    responses=problem_responses(400, 401, 422),
)
async def create_author(svc: Services, body: AuthorCreate, user_id: UserId, response: Response) -> AuthorCard:
    card = await svc.authors.create(body, user_id)
    response.headers["ETag"] = etag(card.version)
    response.headers["Location"] = f"/api/v1/authors/{card.id}"
    return card


@router.put(
    "/{author_id}",
    response_model=AuthorCard,
    summary="Replace an author (optimistic locking)",
    responses=problem_responses(400, 401, 404, 409, 422, 428),
)
async def replace_author(
    svc: Services, author_id: IdPath, body: AuthorReplace, user_id: UserId, if_match: IfMatch, response: Response
) -> AuthorCard:
    card = await svc.authors.replace(author_id, body, if_match, user_id)
    response.headers["ETag"] = etag(card.version)
    return card


@router.patch(
    "/{author_id}",
    response_model=AuthorCard,
    summary="Partially update an author",
    responses=problem_responses(400, 401, 404, 409, 422, 428),
)
async def patch_author(
    svc: Services, author_id: IdPath, body: AuthorPatch, user_id: UserId, if_match: IfMatch, response: Response
) -> AuthorCard:
    card = await svc.authors.patch(author_id, body, if_match, user_id)
    response.headers["ETag"] = etag(card.version)
    return card


@router.delete(
    "/{author_id}",
    status_code=http.HTTP_204_NO_CONTENT,
    summary="Soft-delete an author",
    responses=problem_responses(400, 401, 404, 409),
)
async def delete_author(svc: Services, author_id: IdPath, user_id: UserId, if_match: IfMatch) -> Response:
    await svc.authors.delete(author_id, if_match, user_id)
    return Response(status_code=http.HTTP_204_NO_CONTENT)
