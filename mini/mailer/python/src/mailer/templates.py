"""Tiny ``{{placeholder}}`` template engine for subjects and bodies."""

from __future__ import annotations

import re
from datetime import datetime

__all__ = ["KNOWN_PLACEHOLDERS", "TemplateError", "find_unknown", "render", "template_vars"]

KNOWN_PLACEHOLDERS = frozenset({"date", "datetime", "job"})
_PLACEHOLDER = re.compile(r"\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}")


class TemplateError(ValueError):
    """Raised when a template references an unknown placeholder."""


def find_unknown(template: str) -> list[str]:
    """Return unknown placeholder names used in ``template`` (in order, unique)."""
    seen: list[str] = []
    for match in _PLACEHOLDER.finditer(template):
        name = match.group(1)
        if name not in KNOWN_PLACEHOLDERS and name not in seen:
            seen.append(name)
    return seen


def template_vars(job_name: str, slot_local: datetime) -> dict[str, str]:
    """Build template variables for a job firing at ``slot_local`` (job timezone)."""
    return {
        "date": slot_local.strftime("%Y-%m-%d"),
        "datetime": slot_local.isoformat(timespec="seconds"),
        "job": job_name,
    }


def render(template: str, variables: dict[str, str]) -> str:
    """Substitute ``{{name}}`` placeholders; unknown names raise TemplateError."""

    def _sub(match: re.Match[str]) -> str:
        name = match.group(1)
        try:
            return variables[name]
        except KeyError:
            raise TemplateError(f"unknown placeholder {{{{{name}}}}}") from None

    return _PLACEHOLDER.sub(_sub, template)
