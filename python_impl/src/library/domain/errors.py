"""Domain errors, mapped to RFC 7807 problem responses by the API layer."""

from __future__ import annotations

from typing import Any


class DomainError(Exception):
    status: int = 500
    code: str = "internal-error"
    title: str = "Internal Server Error"

    def __init__(self, detail: str, *, errors: list[dict[str, Any]] | None = None) -> None:
        super().__init__(detail)
        self.detail = detail
        self.errors = errors


class BadRequestError(DomainError):
    status = 400
    code = "bad-request"
    title = "Bad Request"


class UnauthorizedError(DomainError):
    status = 401
    code = "unauthorized"
    title = "Unauthorized"


class NotFoundError(DomainError):
    status = 404
    code = "not-found"
    title = "Not Found"


class ConflictError(DomainError):
    status = 409
    code = "conflict"
    title = "Conflict"


class ValidationFailedError(DomainError):
    status = 422
    code = "validation-error"
    title = "Unprocessable Entity"


class PreconditionRequiredError(DomainError):
    status = 428
    code = "precondition-required"
    title = "Precondition Required"
