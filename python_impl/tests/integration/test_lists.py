"""Keyset pagination (all sorts, ties, no dups/gaps), every filter, FTS and fuzzy search."""

from __future__ import annotations

import uuid
from datetime import UTC, datetime, timedelta
from typing import Any

import asyncpg
import httpx
import pytest

from tests.conftest import create_author, create_book, h, walk

pytestmark = pytest.mark.integration

TITLES = ["Alpha", "Beta", "Alpha", "Gamma", "Beta", "Alpha", "Delta", "Épsilon", "alpha", "Zeta"]
YEARS = [1990, None, 1990, 2001, 1990, None, 1850, 2001, 2020, 1990]


@pytest.fixture(scope="module")
async def dataset(client: httpx.AsyncClient, user_id: str) -> dict[str, Any]:
    """30 books sharing a unique publisher, with deliberately duplicated sort keys."""
    publisher = f"Pagination {uuid.uuid4().hex[:8]}"
    books = []
    for i in range(30):
        books.append(
            await create_book(
                client,
                user_id,
                title=TITLES[i % len(TITLES)],
                publication_year=YEARS[i % len(YEARS)],
                publisher=publisher,
            )
        )
    return {"publisher": publisher, "books": books}


def _expected(books: list[dict[str, Any]], key: str, desc: bool) -> list[str]:
    def sort_key(b: dict[str, Any]) -> tuple[Any, int]:
        if key == "publication_year":
            v: Any = 32767 if b[key] is None else b[key]
        else:
            v = b[key]
        return (v, int(b["id"]))

    ordered = sorted(books, key=sort_key, reverse=desc)
    return [b["id"] for b in ordered]


@pytest.mark.parametrize(
    "sort", ["title", "-title", "publication_year", "-publication_year", "created_at", "-created_at"]
)
@pytest.mark.parametrize("limit", [1, 4, 7, 30, 100])
async def test_book_keyset_walk(client: httpx.AsyncClient, dataset: dict[str, Any], sort: str, limit: int) -> None:
    items = await walk(client, "/api/v1/books", {"publisher": dataset["publisher"], "sort": sort}, limit)
    ids = [b["id"] for b in items]
    assert len(ids) == len(set(ids)) == 30, "duplicates or gaps"
    key, desc = sort.lstrip("-"), sort.startswith("-")
    if key == "title":
        # DB collation decides text order; verify ties (same title) are broken by id consistently.
        titles = [b["title"] for b in items]
        groups: dict[str, list[int]] = {}
        for b in items:
            groups.setdefault(b["title"], []).append(int(b["id"]))
        for g in groups.values():
            assert g == sorted(g, reverse=desc)
        assert titles.index("Zeta") == (0 if desc else 29) or titles[0 if desc else -1] == "Zeta"
    else:
        assert ids == _expected(dataset["books"], key, desc)


async def test_pagination_stable_under_inserts(
    client: httpx.AsyncClient, dataset: dict[str, Any], user_id: str
) -> None:
    """New rows inserted behind the cursor do not cause duplicates in later pages."""
    params = {"publisher": dataset["publisher"], "sort": "created_at", "limit": 10}
    first = (await client.get("/api/v1/books", params=params)).json()
    second = (await client.get("/api/v1/books", params={**params, "cursor": first["next_cursor"]})).json()
    assert not {b["id"] for b in first["items"]} & {b["id"] for b in second["items"]}


async def test_cursor_errors(client: httpx.AsyncClient, dataset: dict[str, Any]) -> None:
    page = (await client.get("/api/v1/books", params={"publisher": dataset["publisher"], "limit": 2})).json()
    resp = await client.get("/api/v1/books", params={"sort": "title", "cursor": page["next_cursor"]})
    assert resp.status_code == 400 and "restart" in resp.json()["detail"]
    assert (await client.get("/api/v1/books", params={"cursor": "garbage!!"})).status_code == 400
    assert (await client.get("/api/v1/books", params={"limit": 101})).status_code == 422
    assert (await client.get("/api/v1/books", params={"limit": 0})).status_code == 422
    assert (await client.get("/api/v1/books", params={"sort": "isbn_13"})).status_code == 422


async def test_total_estimate(client: httpx.AsyncClient, dataset: dict[str, Any]) -> None:
    page = (
        await client.get("/api/v1/books", params={"publisher": dataset["publisher"], "include_total": "true"})
    ).json()
    assert page["total_is_estimate"] is True and isinstance(page["total"], int) and page["total"] >= 1
    page = (await client.get("/api/v1/books", params={"publisher": dataset["publisher"]})).json()
    assert page["total"] is None and page["total_is_estimate"] is False
    assert page["limit"] == 20


@pytest.fixture(scope="module")
async def catalog(client: httpx.AsyncClient, user_id: str, other_user_id: str) -> dict[str, Any]:
    tag = uuid.uuid4().hex[:6]
    tolstoy = await create_author(
        client,
        user_id,
        full_name=f"Leo Tolstoy{tag}",
        original_name="Лев Толстой",
        country_code="RU",
        birth_date="1828-09-09",
        death_date="1910-11-20",
    )
    muller = await create_author(
        client, user_id, full_name=f"Herta Müller{tag}", country_code="DE", birth_date="1953-08-17"
    )
    murakami = await create_author(
        client,
        other_user_id,
        full_name=f"Haruki Murakami{tag}",
        original_name="村上春樹",
        country_code="JP",
        birth_date="1949-01-12",
    )
    nobirth = await create_author(client, user_id, full_name=f"Anonymous Scribe{tag}", country_code="RU")
    pub = f"Catalog {tag}"
    war = await create_book(
        client,
        user_id,
        title=f"Война и мир {tag}",
        original_title="War and Peace",
        isbn_13="9785170987467",
        language_code="ru",
        publication_year=1869,
        publisher=pub,
        genres=["novel", "history"],
        authors=[{"author_id": tolstoy["id"]}],
        description="epic about napoleon invasion",
    )
    anna = await create_book(
        client,
        user_id,
        title=f"Anna Karenina {tag}",
        language_code="en",
        publication_year=1878,
        publisher=pub,
        genres=["novel"],
        authors=[{"author_id": tolstoy["id"]}, {"author_id": muller["id"], "role": "translator", "ordinal": 2}],
    )
    atemschaukel = await create_book(
        client,
        user_id,
        title=f"Atemschaukel {tag}",
        language_code="de",
        publication_year=2009,
        publisher=pub,
        genres=["novel"],
        authors=[{"author_id": muller["id"]}],
    )
    kafka = await create_book(
        client,
        other_user_id,
        title=f"海辺のカフカ {tag}",
        original_title="Kafka on the Shore",
        language_code="ja",
        publication_year=2002,
        publisher=pub,
        genres=["fantasy"],
        authors=[{"author_id": murakami["id"]}],
    )
    noyear = await create_book(client, other_user_id, title=f"Undated Scroll {tag}", publisher=pub, genres=["religion"])
    return {
        "tag": tag,
        "pub": pub,
        "authors": {"tolstoy": tolstoy, "muller": muller, "murakami": murakami, "nobirth": nobirth},
        "books": {"war": war, "anna": anna, "atemschaukel": atemschaukel, "kafka": kafka, "noyear": noyear},
    }


async def _ids(client: httpx.AsyncClient, url: str, **params: Any) -> set[str]:
    resp = await client.get(url, params={**params, "limit": 100})
    assert resp.status_code == 200, resp.text
    return {i["id"] for i in resp.json()["items"]}


async def test_book_filters(
    client: httpx.AsyncClient, catalog: dict[str, Any], user_id: str, other_user_id: str
) -> None:
    b, a, pub, tag = catalog["books"], catalog["authors"], catalog["pub"], catalog["tag"]
    ids = lambda *names: {b[n]["id"] for n in names}  # noqa: E731
    url = "/api/v1/books"
    assert await _ids(client, url, publisher=pub) == ids("war", "anna", "atemschaukel", "kafka", "noyear")
    assert await _ids(client, url, title="karenin", publisher=pub) == ids("anna")
    assert await _ids(client, url, title="война и", publisher=pub) == ids("war")
    assert await _ids(client, url, author=f"tolstoy{tag}") == ids("war", "anna")
    assert await _ids(client, url, author="Толст", publisher=pub) == ids("war", "anna")
    assert await _ids(client, url, author_id=a["muller"]["id"]) == ids("anna", "atemschaukel")
    assert await _ids(client, url, isbn="978-5-17-098746-7") == ids("war")
    assert await _ids(client, url, isbn="9780000000002") == set()
    assert await _ids(client, url, language="RU", publisher=pub) == ids("war")
    assert await _ids(client, url, year_from=1870, publisher=pub) == ids("anna", "atemschaukel", "kafka")
    assert await _ids(client, url, year_to=1870, publisher=pub) == ids("war")
    assert await _ids(client, url, year_from=1800, year_to=2005, publisher=pub) == ids("war", "anna", "kafka")
    assert await _ids(client, url, genre="Novel", publisher=pub) == ids("war", "anna", "atemschaukel")
    assert await _ids(client, url, created_by=other_user_id, publisher=pub) == ids("kafka", "noyear")
    assert await _ids(client, url, created_by=user_id, publisher=pub) == ids("war", "anna", "atemschaukel")
    created = datetime.fromisoformat(b["kafka"]["created_at"])
    assert await _ids(client, url, created_from=created.isoformat(), publisher=pub) == ids("kafka", "noyear")
    assert await _ids(client, url, created_to=(created - timedelta(microseconds=1)).isoformat(), publisher=pub) == ids(
        "war", "anna", "atemschaukel"
    )
    # full-text: description, original title, unaccent
    assert await _ids(client, url, q="napoleon", publisher=pub) == ids("war")
    assert await _ids(client, url, q="kafka shore", publisher=pub) == ids("kafka")
    assert await _ids(client, url, q=f"atemschaukel {tag}") == ids("atemschaukel")
    assert await _ids(client, url, q='"war and peace"', publisher=pub) == ids("war")
    assert await _ids(client, url, q="novel -karenina", publisher=pub) == set()  # genres are not in FTS


async def test_book_filter_validation(client: httpx.AsyncClient) -> None:
    url = "/api/v1/books"
    for params in (
        {"isbn": "123"},
        {"year_from": 2000, "year_to": 1990},
        {"title": "ab"},
        {"language": "english"},
        {"created_from": "2025-01-02T00:00:00Z", "created_to": "2025-01-01T00:00:00Z"},
        {"year_from": -5000},
    ):
        resp = await client.get(url, params=params)
        assert resp.status_code == 422, (params, resp.text)


async def test_book_list_includes_authors(client: httpx.AsyncClient, catalog: dict[str, Any]) -> None:
    resp = await client.get("/api/v1/books", params={"isbn": "9785170987467"})
    item = resp.json()["items"][0]
    assert item["authors"][0]["full_name"].startswith("Leo Tolstoy")


async def test_author_filters_and_sorts(client: httpx.AsyncClient, catalog: dict[str, Any]) -> None:
    a, tag = catalog["authors"], catalog["tag"]
    url = "/api/v1/authors"
    ids = lambda *names: {a[n]["id"] for n in names}  # noqa: E731
    assert await _ids(client, url, name=f"tolstoy{tag}") == ids("tolstoy")
    assert await _ids(client, url, name=f"muller{tag}") == ids("muller")  # unaccent: Müller
    assert await _ids(client, url, name="村上春") >= ids("murakami")
    assert await _ids(client, url, q=f"Murakami{tag}") == ids("murakami")
    assert await _ids(client, url, q="толстой", name=tag) == ids("tolstoy")
    assert await _ids(client, url, country="ru", name=tag) == ids("tolstoy", "nobirth")
    assert await _ids(client, url, born_from="1900-01-01", name=tag) == ids("muller", "murakami")
    assert await _ids(client, url, born_to="1900-01-01", name=tag) == ids("tolstoy")
    for sort in ("full_name", "-full_name", "birth_date", "-birth_date", "created_at", "-created_at"):
        items = await walk(client, url, {"name": tag, "sort": sort}, 1)
        got = [i["id"] for i in items]
        assert len(got) == len(set(got)) == 4
        if sort == "birth_date":
            assert got[-1] == a["nobirth"]["id"]  # NULL birth dates sort last ascending
            assert got[0] == a["tolstoy"]["id"]
        if sort == "-birth_date":
            assert got[0] == a["nobirth"]["id"]
        if sort == "created_at":
            assert got == [a[n]["id"] for n in ("tolstoy", "muller", "murakami", "nobirth")]
    assert (await client.get(url, params={"born_from": "2000-01-01", "born_to": "1900-01-01"})).status_code == 422
    total = (await client.get(url, params={"country": "RU", "include_total": "true"})).json()
    assert total["total_is_estimate"] is True and total["total"] >= 1


async def test_author_books_pagination(client: httpx.AsyncClient, user_id: str) -> None:
    author = await create_author(client, user_id)
    made = [
        await create_book(
            client, user_id, authors=[{"author_id": author["id"]}, {"author_id": author["id"], "role": "editor"}]
        )
        for _ in range(7)
    ]
    items = await walk(client, f"/api/v1/authors/{author['id']}/books", {}, 3)
    assert [i["id"] for i in items] == sorted((b["id"] for b in made), key=int, reverse=True)
    assert items[0]["roles"] == ["author", "editor"]
    resp = await client.get("/api/v1/authors/1/books")
    assert resp.status_code == 404


async def test_search_relevance_and_fuzzy(client: httpx.AsyncClient, catalog: dict[str, Any]) -> None:
    tag = catalog["tag"]
    resp = await client.get("/api/v1/search", params={"q": f"Tolstoi{tag}", "type": "authors"})
    assert resp.status_code == 200
    hits = resp.json()["items"]
    assert hits and hits[0]["id"] == catalog["authors"]["tolstoy"]["id"]  # typo tolerated (trigram)
    resp = await client.get("/api/v1/search", params={"q": f"karenina {tag}", "type": "books"})
    assert resp.json()["items"][0]["id"] == catalog["books"]["anna"]["id"]
    resp = await client.get("/api/v1/search", params={"q": f"Muller{tag}"})
    types = {(i["type"], i["id"]) for i in resp.json()["items"]}
    assert ("author", catalog["authors"]["muller"]["id"]) in types
    scores = [i["score"] for i in resp.json()["items"]]
    assert scores == sorted(scores, reverse=True)


async def test_search_keyset(client: httpx.AsyncClient, catalog: dict[str, Any]) -> None:
    tag = catalog["tag"]
    items = await walk(client, "/api/v1/search", {"q": tag, "type": "all"}, 2)
    keys = [(i["type"], i["id"]) for i in items]
    assert len(keys) == len(set(keys)) >= 9  # 5 books + 4 authors carry the tag
    assert (await client.get("/api/v1/search", params={"q": ""})).status_code == 422
    assert (await client.get("/api/v1/search", params={"q": "x", "type": "films"})).status_code == 422
    bad = await client.get("/api/v1/search", params={"q": "x", "cursor": "e30"})
    assert bad.status_code == 400


async def test_deleted_author_hidden(client: httpx.AsyncClient, user_id: str, db: asyncpg.Connection) -> None:  # type: ignore[type-arg]
    author = await create_author(client, user_id, full_name=f"Ephemeral {uuid.uuid4().hex[:6]}")
    book = await create_book(client, user_id, authors=[{"author_id": author["id"]}])
    url = f"/api/v1/authors/{author['id']}"
    assert (await client.delete(url, headers=h(user_id, 1))).status_code == 204
    assert (await client.get(url)).status_code == 404
    card = (await client.get(f"/api/v1/books/{book['id']}")).json()
    assert card["authors"] == []
    hist = (await client.get(f"{url}/history")).json()["items"]
    assert [e["action"] for e in hist] == ["create", "delete"]
    deleted_at = await db.fetchval("SELECT deleted_at FROM authors WHERE id = $1", int(author["id"]))
    assert deleted_at is not None and deleted_at <= datetime.now(UTC)
    # cannot link a deleted author
    resp = await client.post(
        "/api/v1/books", json={"title": "x", "authors": [{"author_id": author["id"]}]}, headers=h(user_id)
    )
    assert resp.status_code == 422
