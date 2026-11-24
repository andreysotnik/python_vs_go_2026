from datetime import UTC, date, datetime

from library.domain.cursor import Cursor
from library.domain.filters import AuthorFilters, BookFilters
from library.domain.sorting import AUTHOR_SORTS, BOOK_SORTS, Sort
from library.repositories import authors as author_repo
from library.repositories import books as book_repo
from library.repositories.search import build_search_query
from library.repositories.sql import QueryBuilder, like_escape, prefix_tsquery

ALL_BOOK_FILTERS = BookFilters(
    title="war",
    author="tolstoy",
    author_id=7,
    isbn13="9780140447934",
    language="ru",
    year_from=1800,
    year_to=1900,
    publisher="Penguin",
    genre="novel",
    created_by=1,
    created_from=datetime(2020, 1, 1, tzinfo=UTC),
    created_to=datetime(2030, 1, 1, tzinfo=UTC),
    q="war peace",
)


def test_query_builder_placeholders() -> None:
    qb = QueryBuilder().where("a = {}", 1).where("b BETWEEN {} AND {}", 2, 3).where("c IS NULL")
    assert qb.where_sql() == "a = $1 AND b BETWEEN $2 AND $3 AND c IS NULL"
    assert qb.params == [1, 2, 3]
    clone = qb.copy().where("d = {}", 4)
    assert len(qb.params) == 3 and len(clone.params) == 4
    assert QueryBuilder().where_sql() == "TRUE"


def test_book_list_query_all_filters() -> None:
    sort = Sort(BOOK_SORTS["title"], descending=False)
    sql, params = book_repo.build_list_query(ALL_BOOK_FILTERS, sort, None, 20)
    assert "OFFSET" not in sql.upper()
    assert "b.deleted_at IS NULL" in sql
    assert "websearch_to_tsquery" in sql
    assert "book_isbns" in sql
    assert "author_books" in sql
    assert "b.genres @> ARRAY[" in sql
    assert sql.endswith("ORDER BY b.title ASC, b.id ASC LIMIT 21")  # limit + 1 detects the next page
    assert "'tolstoy':*" in params


def test_book_keyset_directions() -> None:
    cur = Cursor(sort="-publication_year", value=1999, id=10)
    sort = Sort(BOOK_SORTS["publication_year"], descending=True)
    sql, params = book_repo.build_list_query(BookFilters(), sort, cur, 5)
    assert "(coalesce(b.publication_year, 32767::smallint), b.id) < ($1::smallint, $2::bigint)" in sql
    assert "DESC, b.id DESC" in sql
    assert params == [1999, 10] and sql.endswith("LIMIT 6")
    sort = Sort(BOOK_SORTS["created_at"], descending=False)
    sql, _ = book_repo.build_list_query(BookFilters(), sort, Cursor("created_at", datetime.now(UTC), 1), 5)
    assert "(b.created_at, b.id) > ($1::timestamptz, $2::bigint)" in sql


def test_year_from_excludes_null_years() -> None:
    qb = book_repo.build_filters(BookFilters(year_from=2000))
    assert "b.publication_year IS NOT NULL" in qb.where_sql()


def test_author_queries() -> None:
    f = AuthorFilters(name="tol_stoy%", country="RU", born_from=date(1800, 1, 1), born_to=date(1900, 1, 1), q="leo")
    sort = Sort(AUTHOR_SORTS["birth_date"], descending=False)
    sql, params = author_repo.build_list_query(f, sort, Cursor("birth_date", date(1850, 1, 1), 3), 10)
    assert "OFFSET" not in sql.upper()
    assert "coalesce(a.birth_date, 'infinity'::date)" in sql
    assert "tol\\_stoy\\%" in params
    sql, params = author_repo.build_books_query(1, None, 10)
    assert "ab.book_id <" not in sql and params == [1] and sql.endswith("LIMIT 11")
    sql, params = author_repo.build_books_query(1, 99, 10)
    assert "ab.book_id < $2" in sql and params == [1, 99]


def test_search_query() -> None:
    sql, params = build_search_query("tolstoy", "all", None, 20, 500)
    assert "UNION ALL" in sql and params == ["tolstoy"] and "LIMIT 500) c" in sql and sql.endswith("LIMIT 21")
    sql, params = build_search_query("x", "books", (0.5, 9), 20, 500)
    assert "UNION ALL" not in sql and "(s.score, s.id) < ($2::float8, $3::bigint)" in sql
    assert params == ["x", 0.5, 9]
    sql, _ = build_search_query("x", "authors", None, 20, 500)
    assert "'author'" in sql and "'book'" not in sql


def test_helpers() -> None:
    assert like_escape("50%_\\") == "50\\%\\_\\\\"
    assert prefix_tsquery("Лев  Толст") == "'лев' & 'толст':*"
    assert prefix_tsquery("o'brien") == "'o' & 'brien':*"
    assert prefix_tsquery("!!!") is None
