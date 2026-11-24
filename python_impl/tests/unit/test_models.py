from datetime import UTC, date, datetime

import pytest
from pydantic import ValidationError

from library.domain.models import AuthorCreate, AuthorPatch, Book, BookCreate, BookPatch, UserCreate


def test_book_isbn_derivation() -> None:
    b = BookCreate(title="x", isbn_10="0-14-044793-8")
    assert b.isbn_13 == "9780140447934"
    b = BookCreate(title="x", isbn_13="978-0-14-044793-4")
    assert b.isbn_10 == "0140447938"
    b = BookCreate(title="x", isbn_13="9791000000008")
    assert b.isbn_10 is None


@pytest.mark.parametrize(
    "fields",
    [
        {"isbn_13": "9780140447935"},
        {"isbn_10": "0140447939"},
        {"isbn_10": "0140447938", "isbn_13": "9780306406157"},
        {"language_code": "russian"},
        {"publication_year": 2500},
        {"page_count": 0},
        {"genres": ["ok", "<script>"]},
        {"genres": [f"g{i}" for i in range(17)]},
        {"external_ids": {"Bad Key": "1"}},
        {"external_ids": {"viaf": ""}},
        {"unknown": 1},
        {"title": ""},
        {"authors": [{"author_id": "1"}, {"author_id": 1}]},
        {"authors": [{"author_id": "abc"}]},
        {"authors": [{"author_id": "1", "role": "ghostwriter"}]},
    ],
)
def test_book_validation_errors(fields: dict[str, object]) -> None:
    with pytest.raises(ValidationError):
        BookCreate.model_validate({"title": "x", **fields})


def test_book_normalisation() -> None:
    b = BookCreate(title="  Title  ", language_code="RU", genres=["Novel", "novel", " Drama "])
    assert b.title == "Title"
    assert b.language_code == "ru"
    assert b.genres == ["novel", "drama"]


def test_patch_rejects_null_title() -> None:
    with pytest.raises(ValidationError):
        BookPatch.model_validate({"title": None})
    assert BookPatch.model_validate({"subtitle": None}).model_fields_set == {"subtitle"}
    with pytest.raises(ValidationError):
        AuthorPatch.model_validate({"full_name": None})


def test_author_validation() -> None:
    a = AuthorCreate(full_name="Leo", country_code="ru", birth_date=date(1828, 9, 9))
    assert a.country_code == "RU"
    with pytest.raises(ValidationError):
        AuthorCreate(full_name="Leo", birth_date=date(1900, 1, 1), death_date=date(1800, 1, 1))
    with pytest.raises(ValidationError):
        AuthorCreate(full_name="Leo", country_code="RUS")
    with pytest.raises(ValidationError):
        AuthorCreate(full_name="Leo", birth_date=date(2200, 1, 1))


def test_ids_serialise_as_strings() -> None:
    now = datetime.now(UTC)
    book = Book(
        id=7395413523316707328,
        title="t",
        original_title=None,
        subtitle=None,
        description=None,
        isbn_10=None,
        isbn_13=None,
        language_code=None,
        publication_year=None,
        publisher=None,
        page_count=None,
        genres=[],
        edition=None,
        external_ids={},
        created_at=now,
        created_by=1,
        updated_at=now,
        updated_by=1,
        version=1,
    )
    dumped = book.model_dump(mode="json")
    assert dumped["id"] == "7395413523316707328" and dumped["created_by"] == "1"


def test_user_validation() -> None:
    UserCreate(username="anna_k", email="a@b.c")
    with pytest.raises(ValidationError):
        UserCreate(username="a", email="a@b.c")
    with pytest.raises(ValidationError):
        UserCreate(username="anna", email="not-an-email")
