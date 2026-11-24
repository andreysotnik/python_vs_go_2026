"""RFC 7807 problem+json responses and exception handlers."""

from __future__ import annotations

import logging
from typing import Any

import asyncpg
import orjson
from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import Response
from starlette.exceptions import HTTPException as StarletteHTTPException

from library.domain.errors import DomainError
from library.domain.models import Problem

PROBLEM_BASE = "https://library.example/problems/"
PROBLEM_CONTENT_TYPE = "application/problem+json"
log = logging.getLogger("library.api")

_HTTP_TITLES = {
    400: "Bad Request",
    401: "Unauthorized",
    404: "Not Found",
    405: "Method Not Allowed",
    409: "Conflict",
    415: "Unsupported Media Type",
    422: "Unprocessable Entity",
    428: "Precondition Required",
    500: "Internal Server Error",
    503: "Service Unavailable",
}


def problem_response(
    status: int,
    code: str,
    detail: str | None,
    instance: str | None,
    errors: list[dict[str, Any]] | None = None,
    title: str | None = None,
) -> Response:
    body: dict[str, Any] = {
        "type": PROBLEM_BASE + code,
        "title": title or _HTTP_TITLES.get(status, "Error"),
        "status": status,
        "detail": detail,
        "instance": instance,
    }
    if errors:
        body["errors"] = errors
    return Response(orjson.dumps(body, default=str), status_code=status, media_type=PROBLEM_CONTENT_TYPE)


def _clean_errors(errors: Any) -> list[dict[str, Any]]:
    out = []
    for e in errors:
        item = {"loc": list(e.get("loc", ())), "msg": e.get("msg"), "type": e.get("type")}
        out.append(item)
    return out


def install_exception_handlers(app: FastAPI) -> None:
    @app.exception_handler(DomainError)
    async def _domain(request: Request, exc: DomainError) -> Response:
        return problem_response(exc.status, exc.code, exc.detail, request.url.path, exc.errors, exc.title)

    @app.exception_handler(RequestValidationError)
    async def _validation(request: Request, exc: RequestValidationError) -> Response:
        errors = _clean_errors(exc.errors())
        if any(e["type"] == "json_invalid" for e in errors):
            return problem_response(400, "malformed-json", "request body is not valid JSON", request.url.path, errors)
        return problem_response(422, "validation-error", "request validation failed", request.url.path, errors)

    @app.exception_handler(StarletteHTTPException)
    async def _http(request: Request, exc: StarletteHTTPException) -> Response:
        code = {404: "not-found", 405: "method-not-allowed"}.get(exc.status_code, "http-error")
        return problem_response(exc.status_code, code, str(exc.detail), request.url.path)

    @app.exception_handler(asyncpg.QueryCanceledError)
    async def _timeout(request: Request, exc: asyncpg.QueryCanceledError) -> Response:
        log.warning("query cancelled", extra={"path": request.url.path, "error": str(exc)})
        return problem_response(503, "query-timeout", "query exceeded statement_timeout", request.url.path)

    @app.exception_handler(asyncpg.CheckViolationError)
    async def _check(request: Request, exc: asyncpg.CheckViolationError) -> Response:
        return problem_response(422, "constraint-violation", str(exc), request.url.path)

    @app.exception_handler(Exception)
    async def _unhandled(request: Request, exc: Exception) -> Response:
        log.exception("unhandled error", extra={"path": request.url.path})
        return problem_response(500, "internal-error", "internal server error", request.url.path)


def problem_responses(*codes: int) -> dict[int | str, dict[str, Any]]:
    """OpenAPI `responses=` entries for problem+json errors."""
    descriptions = {
        400: "Malformed request (bad cursor, unsupported sort, malformed JSON, bad If-Match)",
        401: "Missing/invalid X-User-Id or unknown user",
        404: "Resource not found",
        409: "Version conflict (optimistic locking) or duplicate ISBN/username",
        422: "Validation error",
        428: "If-Match / version required",
        503: "Query timeout or database unavailable",
    }
    return {c: {"model": Problem, "description": descriptions[c], "content": {PROBLEM_CONTENT_TYPE: {}}} for c in codes}
