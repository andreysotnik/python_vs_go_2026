"""Books: CRUD, audit, optimistic locking, soft delete, error mapping."""

from __future__ import annotations

import httpx
import pytest

from tests.conftest import create_author, create_book, h, make_user

pytestmark = pytest.mark.integration


async def test_create_and_get_card(client: httpx.AsyncClient, user_id: str) -> None:
    author = await create_author(client, user_id, full_name="Lev Tolstoy", original_name="Лев Толстой")
    resp = await client.post(
        "/api/v1/books",
        json={
            "title": "Война и мир",
            "original_title": "War and Peace",
            "isbn_10": "0-14-044793-8",
            "language_code": "RU",
            "publication_year": 1869,
            "genres": ["Novel", "History"],
            "external_ids": {"openlibrary": "OL267096W"},
            "authors": [{"author_id": author["id"], "role": "author"}],
        },
        headers=h(user_id),
    )
    assert resp.status_code == 201, resp.text
    book = resp.json()
    assert resp.headers["etag"] == '"1"'
    assert resp.headers["location"] == f"/api/v1/books/{book['id']}"
    assert isinstance(book["id"], str)
    assert book["isbn_13"] == "9780140447934"
    assert book["language_code"] == "ru"
    assert book["genres"] == ["novel", "history"]
    assert book["authors"] == [{"id": author["id"], "full_name": "Lev Tolstoy", "role": "author", "ordinal": 1}]
    assert book["created_by"] == user_id and book["updated_by"] == user_id
    assert book["created_by_username"].startswith("alice_")
    assert book["version"] == 1

    got = await client.get(f"/api/v1/books/{book['id']}")
    assert got.status_code == 200
    assert got.json() == book


async def test_update_flow_with_audit(client: httpx.AsyncClient, user_id: str, other_user_id: str) -> None:
    a1 = await create_author(client, user_id)
    a2 = await create_author(client, user_id)
    book = await create_book(client, user_id, title="Draft", authors=[{"author_id": a1["id"]}])
    url = f"/api/v1/books/{book['id']}"

    # PUT/PATCH without version -> 428
    resp = await client.patch(url, json={"title": "X"}, headers=h(other_user_id))
    assert resp.status_code == 428
    assert resp.headers["content-type"] == "application/problem+json"

    # PATCH with If-Match by another user
    resp = await client.patch(
        url,
        json={"title": "Final", "subtitle": "Sub", "authors": [{"author_id": a2["id"], "role": "translator"}]},
        headers=h(other_user_id, 1),
    )
    assert resp.status_code == 200, resp.text
    patched = resp.json()
    assert patched["version"] == 2 and resp.headers["etag"] == '"2"'
    assert patched["updated_by"] == other_user_id and patched["created_by"] == user_id
    assert patched["updated_at"] > patched["created_at"]
    assert [a["id"] for a in patched["authors"]] == [a2["id"]]

    # stale version -> 409
    resp = await client.patch(url, json={"title": "Stale"}, headers=h(user_id, 1))
    assert resp.status_code == 409
    assert "version" in resp.json()["detail"]

    # PUT with version in body (full replacement clears omitted fields)
    resp = await client.put(url, json={"title": "Replaced", "version": 2}, headers=h(user_id))
    assert resp.status_code == 200, resp.text
    replaced = resp.json()
    assert replaced["subtitle"] is None and replaced["authors"] == [] and replaced["version"] == 3

    # PATCH null clears a nullable field; no-op patch does not bump version
    resp = await client.patch(url, json={"subtitle": None}, headers=h(user_id, 3))
    assert resp.status_code == 200 and resp.json()["version"] == 3

    # author books reflect link replacement
    resp = await client.get(f"/api/v1/authors/{a1['id']}/books")
    assert resp.json()["items"] == []

    hist = (await client.get(f"{url}/history")).json()
    actions = [(e["action"], e["changed_by"]) for e in hist["items"]]
    assert actions == [("create", user_id), ("update", other_user_id), ("update", user_id)]
    upd = hist["items"][1]["diff"]
    assert upd["title"] == {"old": "Draft", "new": "Final"}
    assert upd["authors"]["new"] == [{"author_id": a2["id"], "role": "translator", "ordinal": 1}]
    assert hist["items"][1]["changed_by_username"].startswith("bob_")


async def test_history_pagination(client: httpx.AsyncClient, user_id: str) -> None:
    book = await create_book(client, user_id)
    url = f"/api/v1/books/{book['id']}"
    for v in range(1, 6):
        resp = await client.patch(url, json={"edition": f"ed{v}"}, headers=h(user_id, v))
        assert resp.status_code == 200
    first = (await client.get(f"{url}/history", params={"limit": 4})).json()
    assert len(first["items"]) == 4 and first["next_cursor"]
    second = (await client.get(f"{url}/history", params={"limit": 4, "cursor": first["next_cursor"]})).json()
    assert len(second["items"]) == 2 and second["next_cursor"] is None
    ids = [e["id"] for e in first["items"] + second["items"]]
    assert len(set(ids)) == 6


async def test_soft_delete(client: httpx.AsyncClient, user_id: str) -> None:
    book = await create_book(client, user_id, isbn_13="9780306406157", publisher="SoftDeletePub")
    url = f"/api/v1/books/{book['id']}"
    # duplicate ISBN -> 409
    dup = await client.post("/api/v1/books", json={"title": "dup", "isbn_10": "0306406152"}, headers=h(user_id))
    assert dup.status_code == 409

    assert (await client.delete(url, headers=h(user_id, 99))).status_code == 409
    resp = await client.delete(url, headers=h(user_id))
    assert resp.status_code == 204
    assert (await client.get(url)).status_code == 404
    assert (await client.delete(url, headers=h(user_id))).status_code == 404
    assert (await client.patch(url, json={"title": "x"}, headers=h(user_id, 2))).status_code == 404
    listed = (await client.get("/api/v1/books", params={"publisher": "SoftDeletePub"})).json()
    assert listed["items"] == []
    hist = (await client.get(f"{url}/history")).json()
    assert [e["action"] for e in hist["items"]] == ["create", "delete"]
    # ISBN is released by the soft delete
    again = await create_book(client, user_id, isbn_13="9780306406157")
    assert again["isbn_13"] == "9780306406157"


async def test_isbn_change_and_conflict(client: httpx.AsyncClient, user_id: str) -> None:
    b1 = await create_book(client, user_id, isbn_13="9781861972712")
    b2 = await create_book(client, user_id, isbn_13="9781566199094")
    url2 = f"/api/v1/books/{b2['id']}"
    resp = await client.patch(url2, json={"isbn_13": "9781861972712"}, headers=h(user_id, 1))
    assert resp.status_code == 409
    resp = await client.patch(url2, json={"isbn_13": "9780470059029"}, headers=h(user_id, 1))
    assert resp.status_code == 200
    assert resp.json()["isbn_10"] == "0470059028"
    # old ISBN is free now
    await create_book(client, user_id, isbn_13="9781566199094")
    found = (await client.get("/api/v1/books", params={"isbn": "1-86197-271-7"})).json()["items"]
    assert [b["id"] for b in found] == [b1["id"]]


@pytest.mark.parametrize(
    ("method", "path", "body", "headers", "status"),
    [
        ("post", "/api/v1/books", {"title": "x"}, {}, 401),
        ("post", "/api/v1/books", {"title": "x"}, {"X-User-Id": "abc"}, 401),
        ("post", "/api/v1/books", {"title": "x"}, {"X-User-Id": "123456789"}, 401),
        ("post", "/api/v1/books", {"title": "x", "isbn_13": "9780140447935"}, None, 422),
        ("post", "/api/v1/books", {"title": "x", "publication_year": 3000}, None, 422),
        ("post", "/api/v1/books", {"title": "x", "nope": 1}, None, 422),
        ("post", "/api/v1/books", {"title": "x", "authors": [{"author_id": "123"}]}, None, 422),
        ("post", "/api/v1/books", {}, None, 422),
        ("get", "/api/v1/books/123", None, None, 404),
        ("get", "/api/v1/books/abc", None, None, 422),
        ("get", "/api/v1/books/123/history", None, None, 404),
        ("get", "/api/v1/nowhere", None, None, 404),
        ("put", "/api/v1/books/123", {"title": "x"}, None, 428),
        ("patch", "/api/v1/books/123", {"title": "x"}, {"X-User-Id": "{user}", "If-Match": "nope"}, 400),
    ],
)
async def test_error_mapping(
    client: httpx.AsyncClient,
    user_id: str,
    method: str,
    path: str,
    body: dict[str, object] | None,
    headers: dict[str, str] | None,
    status: int,
) -> None:
    hdrs = h(user_id) if headers is None else {k: v.replace("{user}", user_id) for k, v in headers.items()}
    resp = await client.request(method, path, json=body, headers=hdrs)
    assert resp.status_code == status, resp.text
    problem = resp.json()
    assert resp.headers["content-type"] == "application/problem+json"
    assert problem["status"] == status and problem["type"].startswith("https://") and problem["title"]


async def test_malformed_json_is_400(client: httpx.AsyncClient, user_id: str) -> None:
    resp = await client.post(
        "/api/v1/books", content=b"{bad json", headers={**h(user_id), "content-type": "application/json"}
    )
    assert resp.status_code == 400
    assert resp.json()["type"].endswith("malformed-json")


async def test_validation_problem_lists_fields(client: httpx.AsyncClient, user_id: str) -> None:
    resp = await client.post("/api/v1/books", json={"title": "", "page_count": -1}, headers=h(user_id))
    assert resp.status_code == 422
    locs = {tuple(e["loc"]) for e in resp.json()["errors"]}
    assert ("body", "title") in locs and ("body", "page_count") in locs


async def test_patch_merged_validation(client: httpx.AsyncClient, user_id: str) -> None:
    book = await create_book(client, user_id, isbn_13="9780262033848")
    url = f"/api/v1/books/{book['id']}"
    # isbn_10 that contradicts isbn_13 given in the same patch -> 422
    resp = await client.patch(url, json={"isbn_10": "0306406152", "isbn_13": "9780262033848"}, headers=h(user_id, 1))
    assert resp.status_code == 422
    # clearing isbn_13 also clears the derived isbn_10
    resp = await client.patch(url, json={"isbn_13": None}, headers=h(user_id, 1))
    assert resp.status_code == 200
    assert resp.json()["isbn_10"] is None and resp.json()["isbn_13"] is None


async def test_unknown_user_cannot_write(client: httpx.AsyncClient) -> None:
    ghost = await make_user(client, "ghost")
    resp = await client.post("/api/v1/authors", json={"full_name": "x"}, headers={"X-User-Id": ghost + "1"})
    assert resp.status_code == 401
