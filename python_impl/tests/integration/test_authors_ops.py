"""Authors CRUD + users + operational endpoints + OpenAPI contract."""

from __future__ import annotations

from pathlib import Path

import httpx
import pytest
import yaml

from tests.conftest import create_author, h

pytestmark = pytest.mark.integration
ROOT = Path(__file__).resolve().parents[2]


async def test_author_crud(client: httpx.AsyncClient, user_id: str, other_user_id: str) -> None:
    author = await create_author(
        client,
        user_id,
        full_name="Fyodor Dostoevsky",
        original_name="Фёдор Достоевский",
        birth_date="1821-11-11",
        country_code="RU",
        external_ids={"wikidata": "Q991"},
    )
    assert author["created_by"] == user_id and author["version"] == 1
    url = f"/api/v1/authors/{author['id']}"
    got = await client.get(url)
    assert got.status_code == 200 and got.headers["etag"] == '"1"'
    assert got.json()["created_by_username"].startswith("alice_")

    resp = await client.patch(
        url, json={"death_date": "1881-02-09", "biography": "Novelist"}, headers=h(other_user_id, 1)
    )
    assert resp.status_code == 200, resp.text
    assert resp.json()["updated_by"] == other_user_id and resp.json()["version"] == 2
    # merged document validated: death before birth -> 422
    resp = await client.patch(url, json={"death_date": "1800-01-01"}, headers=h(other_user_id, 2))
    assert resp.status_code == 422
    # stale -> 409
    resp = await client.put(url, json={"full_name": "F. D."}, headers=h(user_id, 1))
    assert resp.status_code == 409
    resp = await client.put(url, json={"full_name": "F. M. Dostoevsky", "version": 2}, headers=h(user_id))
    assert resp.status_code == 200
    body = resp.json()
    assert body["original_name"] is None and body["external_ids"] == {} and body["version"] == 3

    hist = (await client.get(f"{url}/history")).json()["items"]
    assert [e["action"] for e in hist] == ["create", "update", "update"]
    assert hist[1]["diff"]["death_date"] == {"old": None, "new": "1881-02-09"}
    assert hist[0]["diff"]["full_name"] == "Fyodor Dostoevsky"

    assert (await client.delete(url, headers=h(user_id, 1))).status_code == 409
    assert (await client.delete(url, headers=h(user_id, 3))).status_code == 204
    assert (await client.get(url)).status_code == 404
    assert (await client.get(f"{url}/history")).status_code == 200
    assert (await client.get("/api/v1/authors/42/history")).status_code == 404
    assert (await client.patch("/api/v1/authors/42", json={}, headers=h(user_id, 1))).status_code == 404
    assert (await client.delete("/api/v1/authors/42", headers=h(user_id))).status_code == 404


async def test_author_validation(client: httpx.AsyncClient, user_id: str) -> None:
    for body in (
        {"full_name": ""},
        {"full_name": "x", "country_code": "Russia"},
        {"full_name": "x", "birth_date": "1900-01-01", "death_date": "1899-01-01"},
        {"full_name": "x", "external_ids": {"VIAF!": "1"}},
    ):
        resp = await client.post("/api/v1/authors", json=body, headers=h(user_id))
        assert resp.status_code == 422, body


async def test_users(client: httpx.AsyncClient) -> None:
    resp = await client.post("/api/v1/users", json={"username": "carol_unique", "email": "carol@example.org"})
    assert resp.status_code == 201
    uid = resp.json()["id"]
    assert (await client.get(f"/api/v1/users/{uid}")).json()["username"] == "carol_unique"
    dup = await client.post("/api/v1/users", json={"username": "CAROL_unique", "email": "c2@example.org"})
    assert dup.status_code == 409
    assert (await client.post("/api/v1/users", json={"username": "x", "email": "bad"})).status_code == 422
    assert (await client.get("/api/v1/users/5")).status_code == 404


async def test_ops_endpoints(client: httpx.AsyncClient) -> None:
    assert (await client.get("/healthz")).json() == {"status": "ok", "detail": None}
    assert (await client.get("/readyz")).json()["status"] == "ok"
    await client.get("/api/v1/books", params={"limit": 1})
    metrics = await client.get("/metrics")
    assert metrics.status_code == 200
    assert 'http_requests_total{method="GET",route="/api/v1/books"' in metrics.text
    resp = await client.get("/api/v1/books", headers={"X-Request-Id": "req-123"})
    assert resp.headers["x-request-id"] == "req-123"


async def test_openapi_file_matches_app(client: httpx.AsyncClient) -> None:
    served = yaml.safe_load((await client.get("/openapi.yaml")).text)
    assert served["openapi"].startswith("3.1")
    committed = yaml.safe_load((ROOT / "openapi.yaml").read_text())
    assert committed == served, "openapi.yaml is stale: run `make openapi`"
    docs = await client.get("/docs")
    assert docs.status_code == 200 and "swagger-ui" in docs.text
