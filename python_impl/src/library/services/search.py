"""Cross-entity relevance search."""

from __future__ import annotations

from library.db import Database
from library.domain.cursor import decode_cursor
from library.domain.models import Page, SearchHit
from library.repositories.search import SearchRepository, SearchType
from library.services.common import clamp_limit, paginate

SEARCH_SORT = "-relevance"


class SearchService:
    def __init__(self, db: Database, repo: SearchRepository, default_limit: int, max_candidates: int) -> None:
        self.db = db
        self.repo = repo
        self.default_limit = default_limit
        self.max_candidates = max_candidates

    async def search(self, q: str, type_: SearchType, cursor_raw: str | None, limit: int | None) -> Page[SearchHit]:
        cursor = decode_cursor(cursor_raw, SEARCH_SORT, "float") if cursor_raw else None
        lim = clamp_limit(limit, self.default_limit)
        after = (float(cursor.value), cursor.id) if cursor else None  # type: ignore[arg-type]
        async with self.db.tx(readonly=True) as conn:
            rows = await self.repo.search(conn, q, type_, after, lim, self.max_candidates)
        items, next_cursor = paginate(
            rows, lim, lambda r: SearchHit.model_validate(dict(r)), SEARCH_SORT, lambda r: r["score"]
        )
        return Page[SearchHit](items=items, next_cursor=next_cursor, limit=lim, total=None, total_is_estimate=False)
