"""Tiny parametrised SQL builder: collects WHERE clauses and positional ($n) params."""

from __future__ import annotations

from typing import Any

from library.domain.cursor import Cursor
from library.domain.sorting import Sort


def like_escape(value: str) -> str:
    return value.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")


class QueryBuilder:
    def __init__(self) -> None:
        self.params: list[Any] = []
        self.clauses: list[str] = []

    def param(self, value: Any) -> str:
        self.params.append(value)
        return f"${len(self.params)}"

    def where(self, clause: str, *values: Any) -> QueryBuilder:
        """Add a clause; each '{}' is replaced by a fresh positional parameter."""
        placeholders = [self.param(v) for v in values]
        self.clauses.append(clause.format(*placeholders))
        return self

    def where_sql(self) -> str:
        return " AND ".join(self.clauses) if self.clauses else "TRUE"

    def copy(self) -> QueryBuilder:
        clone = QueryBuilder()
        clone.params = list(self.params)
        clone.clauses = list(self.clauses)
        return clone


def keyset_clause(qb: QueryBuilder, sort: Sort, cursor: Cursor | None, id_expr: str) -> None:
    """Row-value comparison '(key, id) > (v, id)' - a single index range condition."""
    if cursor is None:
        return
    op = "<" if sort.descending else ">"
    value = qb.param(cursor.value)
    last_id = qb.param(cursor.id)
    qb.clauses.append(f"({sort.field.expr}, {id_expr}) {op} ({value}::{sort.field.cast}, {last_id}::bigint)")


def order_by(sort: Sort, id_expr: str) -> str:
    direction = "DESC" if sort.descending else "ASC"
    return f"ORDER BY {sort.field.expr} {direction}, {id_expr} {direction}"


def prefix_tsquery(text: str) -> str | None:
    """Build a to_tsquery() string: all words AND-ed, the last one as a prefix ('tols' -> 'tols:*')."""
    words = [w for w in "".join(ch if ch.isalnum() else " " for ch in text).split() if w]
    if not words:
        return None
    terms = [f"'{w.lower()}'" for w in words]
    terms[-1] += ":*"
    return " & ".join(terms)
