"""Pluggable authentication. Today: trusted X-User-Id header; tomorrow: swap in a JWT authenticator."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Annotated, Protocol

from fastapi import Request, Security
from fastapi.security import APIKeyHeader
from starlette.types import ASGIApp, Receive, Scope, Send

from library.api.problems import problem_response
from library.domain.errors import UnauthorizedError


@dataclass(frozen=True, slots=True)
class Principal:
    user_id: int


class AuthError(Exception):
    pass


class Authenticator(Protocol):
    def authenticate(self, headers: dict[str, str]) -> Principal | None:
        """Return a principal, None for anonymous, or raise AuthError for bad credentials."""
        ...


class HeaderAuthenticator:
    header = "x-user-id"

    def authenticate(self, headers: dict[str, str]) -> Principal | None:
        raw = headers.get(self.header)
        if raw is None or raw == "":
            return None
        raw = raw.strip()
        if not raw.isdigit() or len(raw) > 19 or int(raw) <= 0:
            raise AuthError("X-User-Id must be a positive integer id")
        return Principal(user_id=int(raw))


class AuthMiddleware:
    def __init__(self, app: ASGIApp, authenticator: Authenticator) -> None:
        self.app = app
        self.authenticator = authenticator

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return
        headers = {k.decode("latin-1"): v.decode("latin-1") for k, v in scope["headers"]}
        try:
            principal = self.authenticator.authenticate(headers)
        except AuthError as exc:
            response = problem_response(401, "unauthorized", str(exc), scope.get("path"))
            await response(scope, receive, send)
            return
        scope.setdefault("state", {})["principal"] = principal
        await self.app(scope, receive, send)


# Declared as a security scheme so Swagger UI shows an "Authorize" button for X-User-Id.
user_id_header = APIKeyHeader(name="X-User-Id", auto_error=False, description="id of the acting user")


def require_user(request: Request, _header: Annotated[str | None, Security(user_id_header)] = None) -> int:
    principal: Principal | None = getattr(request.state, "principal", None)
    if principal is None:
        raise UnauthorizedError("X-User-Id header is required for write operations")
    return principal.user_id
