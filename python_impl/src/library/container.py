"""Explicit dependency wiring (no globals): built once per app in the lifespan."""

from __future__ import annotations

from dataclasses import dataclass

from library.config import Settings
from library.db import Database
from library.repositories.audit import AuditRepository
from library.repositories.authors import AuthorRepository
from library.repositories.books import BookRepository
from library.repositories.search import SearchRepository
from library.repositories.users import UserRepository
from library.services.authors import AuthorService
from library.services.books import BookService
from library.services.search import SearchService
from library.services.users import UserService


@dataclass(frozen=True, slots=True)
class Container:
    db: Database
    books: BookService
    authors: AuthorService
    search: SearchService
    users: UserService


def build_container(db: Database, settings: Settings) -> Container:
    book_repo, author_repo = BookRepository(), AuthorRepository()
    audit_repo, user_repo = AuditRepository(), UserRepository()
    limit = settings.default_page_size
    return Container(
        db=db,
        books=BookService(db, book_repo, author_repo, audit_repo, user_repo, limit),
        authors=AuthorService(db, author_repo, book_repo, audit_repo, user_repo, limit),
        search=SearchService(db, SearchRepository(), limit, settings.search_max_candidates),
        users=UserService(db, user_repo),
    )
