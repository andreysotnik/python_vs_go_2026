"""Sort whitelists. Every sortable field is backed by a composite "(key, id)" index."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Literal

from library.domain.errors import BadRequestError

ValueKind = Literal["text", "int", "timestamp", "date", "float"]


@dataclass(frozen=True, slots=True)
class SortField:
    name: str
    # SQL expression - must be identical to the index expression so the planner can use it.
    expr: str
    kind: ValueKind
    cast: str


@dataclass(frozen=True, slots=True)
class Sort:
    field: SortField
    descending: bool

    @property
    def key(self) -> str:
        return ("-" if self.descending else "") + self.field.name


BOOK_SORTS: dict[str, SortField] = {
    "title": SortField("title", "b.title", "text", "text"),
    # NULL years sort as 32767 (i.e. last ascending / first descending) - see books_year_idx.
    "publication_year": SortField(
        "publication_year", "coalesce(b.publication_year, 32767::smallint)", "int", "smallint"
    ),
    "created_at": SortField("created_at", "b.created_at", "timestamp", "timestamptz"),
}

AUTHOR_SORTS: dict[str, SortField] = {
    "full_name": SortField("full_name", "a.full_name", "text", "text"),
    # NULL birth dates sort as +infinity - see authors_birth_idx.
    "birth_date": SortField("birth_date", "coalesce(a.birth_date, 'infinity'::date)", "date", "date"),
    "created_at": SortField("created_at", "a.created_at", "timestamp", "timestamptz"),
}


def parse_sort(raw: str | None, allowed: dict[str, SortField], default: str) -> Sort:
    value = (raw or default).strip()
    descending = value.startswith("-")
    name = value.lstrip("+-")
    field = allowed.get(name)
    if field is None:
        raise BadRequestError(f"unsupported sort {value!r}; allowed: " + ", ".join(f"{n}, -{n}" for n in allowed))
    return Sort(field=field, descending=descending)
