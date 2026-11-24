"""Search, users and operational endpoints."""

from __future__ import annotations

from typing import Annotated, Literal

import yaml
from fastapi import APIRouter, Query, Request, Response
from fastapi import status as http

from library.api.deps import CursorQ, IdPath, Limit, Services
from library.api.observability import render_metrics
from library.api.problems import problem_responses
from library.domain.models import Health, Page, SearchHit, User, UserCreate

search_router = APIRouter(prefix="/api/v1", tags=["search"])
users_router = APIRouter(prefix="/api/v1/users", tags=["users"])
ops_router = APIRouter(tags=["ops"])


@search_router.get(
    "/search",
    response_model=Page[SearchHit],
    summary="Relevance search over books and authors",
    responses=problem_responses(400, 422, 503),
)
async def search(
    svc: Services,
    q: Annotated[str, Query(min_length=1, max_length=200, description="query text; typos tolerated (trigram)")],
    type: Annotated[Literal["books", "authors", "all"], Query(description="what to search")] = "all",
    limit: Limit = None,
    cursor: CursorQ = None,
) -> Page[SearchHit]:
    return await svc.search.search(q, type, cursor, limit)


@users_router.post(
    "",
    response_model=User,
    status_code=http.HTTP_201_CREATED,
    summary="Register a user",
    responses=problem_responses(400, 409, 422),
)
async def create_user(svc: Services, body: UserCreate, response: Response) -> User:
    user = await svc.users.create(body)
    response.headers["Location"] = f"/api/v1/users/{user.id}"
    return user


@users_router.get("/{user_id}", response_model=User, summary="Get a user", responses=problem_responses(404, 422))
async def get_user(svc: Services, user_id: IdPath) -> User:
    return await svc.users.get(user_id)


@ops_router.get("/healthz", response_model=Health, summary="Liveness probe")
async def healthz() -> Health:
    return Health(status="ok")


@ops_router.get(
    "/readyz",
    response_model=Health,
    summary="Readiness probe (checks the database)",
    responses={503: {"model": Health, "description": "database unavailable"}},
)
async def readyz(svc: Services, response: Response) -> Health:
    try:
        await svc.db.ping()
    except Exception as exc:  # noqa: BLE001 - any DB failure means "not ready"
        response.status_code = http.HTTP_503_SERVICE_UNAVAILABLE
        return Health(status="unavailable", detail=type(exc).__name__)
    return Health(status="ok")


@ops_router.get("/metrics", summary="Prometheus metrics", response_class=Response)
async def metrics() -> Response:
    return Response(render_metrics(), media_type="text/plain; version=0.0.4; charset=utf-8")


@ops_router.get(
    "/openapi.yaml", summary="OpenAPI 3.1 document (YAML)", response_class=Response, include_in_schema=False
)
async def openapi_yaml(request: Request) -> Response:
    spec = request.app.openapi()
    return Response(yaml.safe_dump(spec, sort_keys=False, allow_unicode=True), media_type="application/yaml")
