//! Integration tests against a real PostgreSQL (see tests/common).

mod common;

use std::collections::HashSet;

use axum::http::{Method, StatusCode};
use common::{App, app, id, ids, random_isbn, token};
use serde_json::{Value, json};

fn book(title: &str) -> Value {
    json!({"title": title, "language_code": "en"})
}

// ---------------------------------------------------------------------------
// system endpoints
// ---------------------------------------------------------------------------

#[tokio::test]
async fn system_endpoints() {
    let a = app().await;
    let r = a.get("/healthz").await;
    assert_eq!(r.status, StatusCode::OK);
    assert_eq!(r.body["status"], "ok");
    let r = a.get("/readyz").await;
    assert_eq!(r.status, StatusCode::OK, "{}", r.raw);
    let _ = a.get("/api/v1/books?limit=1").await;
    let r = a.get("/metrics").await;
    assert_eq!(r.status, StatusCode::OK);
    assert!(r.raw.contains("world_library_http_requests_total"), "{}", r.raw);
    assert!(r.raw.contains("route=\"/api/v1/books\""));
    let r = a.get("/openapi.yaml").await;
    assert_eq!(r.status, StatusCode::OK);
    assert!(r.raw.starts_with("openapi: 3.1"));
    let r = a.get("/docs").await;
    assert_eq!(r.status, StatusCode::OK);
    assert!(r.raw.contains("swagger-ui"));
    assert!(r.headers.get("x-request-id").is_some());
}

#[tokio::test]
async fn unknown_routes_and_methods_are_problems() {
    let a = app().await;
    let r = a.get("/api/v1/nope").await;
    assert_eq!(r.status, StatusCode::NOT_FOUND);
    assert_eq!(r.headers["content-type"], "application/problem+json");
    assert!(r.body["type"].as_str().unwrap().ends_with("route-not-found"));
    let r = a.req(Method::DELETE, "/api/v1/books", None, &[]).await;
    assert_eq!(r.status, StatusCode::METHOD_NOT_ALLOWED);
    assert_eq!(r.body["status"], 405);
}

/// Every operation documented in openapi.yaml is routed (no 404 route-not-found / 405).
#[tokio::test]
async fn openapi_matches_router() {
    let a = app().await;
    let spec: serde_yaml::Value = serde_yaml::from_str(world_library::http::OPENAPI_YAML).expect("valid yaml");
    let paths = spec["paths"].as_mapping().expect("paths");
    let mut checked = 0;
    for (path, ops) in paths {
        let path = path.as_str().unwrap();
        let uri = path.replace("{id}", "999999999");
        for (method, _) in ops.as_mapping().unwrap() {
            let m = method.as_str().unwrap();
            let Ok(method) = m.to_uppercase().parse::<Method>() else { continue };
            if !["GET", "POST", "PUT", "PATCH", "DELETE"].contains(&method.as_str()) {
                continue;
            }
            let full = if path.starts_with("/api") || path.starts_with('/') { uri.clone() } else { format!("/{uri}") };
            let r = a.req(method.clone(), &full, Some(json!({})), &[]).await;
            assert_ne!(r.status, StatusCode::METHOD_NOT_ALLOWED, "{method} {full}");
            let route_missing = r.body["type"].as_str().is_some_and(|t| t.ends_with("route-not-found"));
            assert!(!route_missing, "{method} {full} documented but not routed");
            checked += 1;
        }
    }
    assert!(checked >= 20, "only {checked} operations in spec");
}

// ---------------------------------------------------------------------------
// users & auth
// ---------------------------------------------------------------------------

#[tokio::test]
async fn users_crud_and_errors() {
    let a = app().await;
    let t = token();
    let r = a.req(Method::POST, "/api/v1/users", Some(json!({"username": format!("u_{t}"), "email": format!("{t}@x.org")})), &[]).await;
    assert_eq!(r.status, StatusCode::CREATED, "{}", r.raw);
    let uid = id(&r.body);
    assert!(r.headers["location"].to_str().unwrap().ends_with(&uid.to_string()));
    let r = a.get(&format!("/api/v1/users/{uid}")).await;
    assert_eq!(r.status, StatusCode::OK);
    assert_eq!(r.body["username"], format!("u_{t}"));
    let r =
        a.req(Method::POST, "/api/v1/users", Some(json!({"username": format!("U_{t}"), "email": format!("other{t}@x.org")})), &[]).await;
    assert_eq!(r.status, StatusCode::CONFLICT, "{}", r.raw);
    let r = a.req(Method::POST, "/api/v1/users", Some(json!({"username": "a b", "email": "bad"})), &[]).await;
    assert_eq!(r.status, StatusCode::UNPROCESSABLE_ENTITY);
    assert_eq!(r.body["errors"].as_array().unwrap().len(), 2);
    assert_eq!(a.get("/api/v1/users/123").await.status, StatusCode::NOT_FOUND);
}

#[tokio::test]
async fn writes_require_valid_actor() {
    let a = app().await;
    let r = a.req(Method::POST, "/api/v1/books", Some(book("No actor")), &[]).await;
    assert_eq!(r.status, StatusCode::UNAUTHORIZED);
    assert_eq!(r.headers["content-type"], "application/problem+json");
    let r = a.req(Method::POST, "/api/v1/books", Some(book("Bad actor")), &[("x-user-id", "abc".into())]).await;
    assert_eq!(r.status, StatusCode::UNAUTHORIZED);
    let r = a.post_as("/api/v1/books", book("Unknown actor"), 424242).await;
    assert_eq!(r.status, StatusCode::UNAUTHORIZED, "{}", r.raw);
    let r = a.post_as("/api/v1/authors", json!({"full_name": "Ghost"}), 424242).await;
    assert_eq!(r.status, StatusCode::UNAUTHORIZED, "{}", r.raw);
}

// ---------------------------------------------------------------------------
// authors CRUD + audit
// ---------------------------------------------------------------------------

#[tokio::test]
async fn author_crud_audit_and_locking() {
    let a = app().await;
    let editor = a.create_user().await;
    let t = token();
    let r = a
        .post(
            "/api/v1/authors",
            json!({
                "full_name": format!("Leo Tolstoy {t}"), "original_name": "Лев Толстой",
                "birth_date": "1828-09-09", "death_date": "1910-11-20", "country_code": "ru",
                "external_ids": {"viaf": "96987389", "wikidata": "Q7243"}
            }),
        )
        .await;
    assert_eq!(r.status, StatusCode::CREATED, "{}", r.raw);
    assert_eq!(r.headers["etag"], "\"1\"");
    let aid = id(&r.body);
    assert_eq!(r.body["country_code"], "RU");
    assert_eq!(r.body["created_by"]["id"], "1");
    assert_eq!(r.body["created_by"]["username"], "system");
    assert_eq!(r.body["version"], 1);

    let uri = format!("/api/v1/authors/{aid}");
    let r = a.get(&uri).await;
    assert_eq!(r.status, StatusCode::OK);
    assert_eq!(r.body["original_name"], "Лев Толстой");

    // PATCH by another user, version via If-Match
    let r = a.patch_as(&uri, json!({"biography": "Russian writer", "death_date": null}), Some(1), editor).await;
    assert_eq!(r.status, StatusCode::OK, "{}", r.raw);
    assert_eq!(r.body["version"], 2);
    assert_eq!(r.body["updated_by"]["id"], editor.to_string());
    assert_eq!(r.body["created_by"]["id"], "1");
    assert!(r.body["death_date"].is_null());

    // stale version -> 409, missing version -> 428
    assert_eq!(a.patch(&uri, json!({"biography": "x"}), Some(1)).await.status, StatusCode::CONFLICT);
    assert_eq!(a.patch(&uri, json!({"biography": "x"}), None).await.status, StatusCode::PRECONDITION_REQUIRED);
    // version in body works too
    let r = a.req(Method::PATCH, &uri, Some(json!({"biography": "Novelist", "version": 2})), &[("x-user-id", "1".into())]).await;
    assert_eq!(r.status, StatusCode::OK, "{}", r.raw);
    assert_eq!(r.body["version"], 3);

    // PUT replaces everything
    let r = a.put(&uri, json!({"full_name": format!("Lev Tolstoy {t}"), "country_code": "RU"}), Some(3)).await;
    assert_eq!(r.status, StatusCode::OK, "{}", r.raw);
    assert!(r.body["biography"].is_null());
    assert!(r.body["original_name"].is_null());
    assert_eq!(r.body["version"], 4);

    // validation
    let r = a.patch(&uri, json!({"birth_date": "1950-01-01", "death_date": "1900-01-01"}), Some(4)).await;
    assert_eq!(r.status, StatusCode::UNPROCESSABLE_ENTITY);
    let r = a.patch(&uri, json!({"birth_date": "not-a-date"}), Some(4)).await;
    assert_eq!(r.status, StatusCode::UNPROCESSABLE_ENTITY);

    // delete (soft) with wrong If-Match -> 409, then OK
    let r = a.req(Method::DELETE, &uri, None, &[("x-user-id", "1".into()), ("if-match", "\"1\"".into())]).await;
    assert_eq!(r.status, StatusCode::CONFLICT);
    assert_eq!(a.delete(&uri).await.status, StatusCode::NO_CONTENT);
    assert_eq!(a.get(&uri).await.status, StatusCode::NOT_FOUND);
    assert_eq!(a.delete(&uri).await.status, StatusCode::NOT_FOUND);
    assert_eq!(a.patch(&uri, json!({"biography": "x"}), Some(5)).await.status, StatusCode::NOT_FOUND);

    // history survives soft delete
    let r = a.get(&format!("{uri}/history")).await;
    assert_eq!(r.status, StatusCode::OK, "{}", r.raw);
    let items = r.body["items"].as_array().unwrap();
    let actions: Vec<&str> = items.iter().map(|i| i["action"].as_str().unwrap()).collect();
    assert_eq!(actions, vec!["create", "update", "update", "update", "delete"]);
    assert_eq!(items[1]["changed_by"]["id"], editor.to_string());
    assert_eq!(items[1]["diff"]["biography"]["new"], "Russian writer");
    assert_eq!(items[0]["diff"]["original_name"], "Лев Толстой");
    // DB row is still there, marked deleted
    let (deleted,): (bool,) =
        sqlx::query_as("SELECT deleted_at IS NOT NULL FROM authors WHERE id = $1").bind(aid).fetch_one(&a.pool).await.unwrap();
    assert!(deleted);
}

// ---------------------------------------------------------------------------
// books CRUD + audit
// ---------------------------------------------------------------------------

#[tokio::test]
async fn book_crud_audit_isbn_and_locking() {
    let a = app().await;
    let t = token();
    let au1 = id(&a.create_author(json!({"full_name": format!("Author One {t}")})).await);
    let au2 = id(&a.create_author(json!({"full_name": format!("Translator Two {t}")})).await);
    let isbn = random_isbn();
    let r = a
        .post(
            "/api/v1/books",
            json!({
                "title": format!("War and Peace {t}"), "original_title": "Война и мир", "language_code": "EN",
                "isbn_13": isbn, "publication_year": 1869, "publisher": "Russkiy Vestnik", "page_count": 1225,
                "genres": ["Novel", "historical fiction"], "external_ids": {"openlibrary": "OL1M"},
                "authors": [{"author_id": au1.to_string()}, {"author_id": au2.to_string(), "role": "translator"}]
            }),
        )
        .await;
    assert_eq!(r.status, StatusCode::CREATED, "{}", r.raw);
    let b = r.body;
    let bid = id(&b);
    let uri = format!("/api/v1/books/{bid}");
    assert_eq!(b["language_code"], "en");
    assert_eq!(b["isbn_13"], isbn);
    assert!(b["isbn_10"].is_null(), "979 ISBNs have no ISBN-10");
    assert_eq!(b["genres"], json!(["novel", "historical fiction"]));
    assert_eq!(b["authors"].as_array().unwrap().len(), 2);
    assert_eq!(b["authors"][0]["role"], "author");
    assert_eq!(b["authors"][1]["full_name"], format!("Translator Two {t}"));
    assert_eq!(b["created_by"]["username"], "system");
    assert!(b["created_at"].is_string() && b["updated_at"].is_string());

    // duplicate ISBN -> 409 (global uniqueness via isbn_registry)
    let r = a.post("/api/v1/books", json!({"title": "Dup", "language_code": "en", "isbn_13": isbn})).await;
    assert_eq!(r.status, StatusCode::CONFLICT, "{}", r.raw);

    // unknown author -> 422
    let r = a.post("/api/v1/books", json!({"title": "X", "language_code": "en", "authors": [{"author_id": "12345"}]})).await;
    assert_eq!(r.status, StatusCode::UNPROCESSABLE_ENTITY);
    assert_eq!(r.body["errors"][0]["field"], "authors[0].author_id");

    // PATCH: drop translator, change title
    let editor = a.create_user().await;
    let r =
        a.patch_as(&uri, json!({"title": format!("Voina i Mir {t}"), "authors": [{"author_id": au1.to_string()}]}), Some(1), editor).await;
    assert_eq!(r.status, StatusCode::OK, "{}", r.raw);
    assert_eq!(r.headers["etag"], "\"2\"");
    assert_eq!(r.body["authors"].as_array().unwrap().len(), 1);
    assert_eq!(r.body["updated_by"]["id"], editor.to_string());
    assert_eq!(r.body["publisher"], "Russkiy Vestnik");

    // both directions of the link are maintained
    let r = a.get(&format!("/api/v1/authors/{au2}/books")).await;
    assert_eq!(r.body["items"].as_array().unwrap().len(), 0);
    let r = a.get(&format!("/api/v1/authors/{au1}/books")).await;
    assert_eq!(ids(r.body["items"].as_array().unwrap()), vec![bid]);

    // optimistic locking
    let r = a.patch(&uri, json!({"title": "Stale"}), Some(1)).await;
    assert_eq!(r.status, StatusCode::CONFLICT);
    assert_eq!(r.headers["content-type"], "application/problem+json");
    assert_eq!(a.patch(&uri, json!({"title": "No version"}), None).await.status, StatusCode::PRECONDITION_REQUIRED);
    assert_eq!(a.put(&uri, book("No version"), None).await.status, StatusCode::PRECONDITION_REQUIRED);
    let r = a.req(Method::PATCH, &uri, Some(json!({"title": "x"})), &[("x-user-id", "1".into()), ("if-match", "*".into())]).await;
    assert_eq!(r.status, StatusCode::BAD_REQUEST);

    // PUT with ISBN-10 -> ISBN-13 derived; old ISBN released
    let r =
        a.put(&uri, json!({"title": format!("Peace {t}"), "language_code": "ru", "isbn_10": "0-306-40615-2", "version": 2}), None).await;
    if r.status == StatusCode::CONFLICT {
        // another test run may already own this well-known ISBN; use a random one instead
        let r = a.put(&uri, json!({"title": format!("Peace {t}"), "language_code": "ru", "version": 2}), None).await;
        assert_eq!(r.status, StatusCode::OK, "{}", r.raw);
    } else {
        assert_eq!(r.status, StatusCode::OK, "{}", r.raw);
        assert_eq!(r.body["isbn_13"], "9780306406157");
        assert!(r.body["authors"].as_array().unwrap().is_empty());
    }
    let r = a.post("/api/v1/books", json!({"title": "Reuse", "language_code": "en", "isbn_13": isbn})).await;
    assert_eq!(r.status, StatusCode::CREATED, "released ISBN can be reused: {}", r.raw);
    let reuse_id = id(&r.body);

    // history
    let r = a.get(&format!("{uri}/history")).await;
    let items = r.body["items"].as_array().unwrap().clone();
    let actions: Vec<&str> = items.iter().map(|i| i["action"].as_str().unwrap()).collect();
    assert_eq!(actions, vec!["create", "update", "update"]);
    assert_eq!(items[0]["changed_by"]["username"], "system");
    assert_eq!(items[1]["changed_by"]["id"], editor.to_string());
    assert_eq!(items[1]["diff"]["title"]["old"], format!("War and Peace {t}"));
    assert!(items[1]["diff"]["authors"].is_object());
    assert!(items[1]["diff"].get("publisher").is_none());
    // paginated history
    let all = a.collect_pages(&format!("{uri}/history?limit=1")).await;
    assert_eq!(all.len(), 3);

    // soft delete
    assert_eq!(a.delete(&uri).await.status, StatusCode::NO_CONTENT);
    assert_eq!(a.get(&uri).await.status, StatusCode::NOT_FOUND);
    let r = a.get(&format!("/api/v1/books?q=Peace {t}")).await;
    assert!(r.body["items"].as_array().unwrap().is_empty());
    let r = a.get(&format!("{uri}/history")).await;
    assert_eq!(r.body["items"].as_array().unwrap().last().unwrap()["action"], "delete");
    assert_eq!(a.delete(&format!("/api/v1/books/{reuse_id}")).await.status, StatusCode::NO_CONTENT);
    assert_eq!(a.get("/api/v1/books/999999999/history").await.status, StatusCode::NOT_FOUND);
}

#[tokio::test]
async fn validation_and_bad_requests() {
    let a = app().await;
    // 422: semantic validation, all problems reported at once
    let r = a
        .post(
            "/api/v1/books",
            json!({"title": "", "language_code": "english", "publication_year": 3000,
        "isbn_13": "9780000000000", "page_count": 0, "genres": ["a/b"]}),
        )
        .await;
    assert_eq!(r.status, StatusCode::UNPROCESSABLE_ENTITY);
    assert_eq!(r.headers["content-type"], "application/problem+json");
    let fields: HashSet<&str> = r.body["errors"].as_array().unwrap().iter().map(|e| e["field"].as_str().unwrap()).collect();
    for f in ["title", "language_code", "publication_year", "isbn_13", "page_count", "genres"] {
        assert!(fields.contains(f), "{f} missing: {}", r.raw);
    }
    // 422: wrong types / missing fields / unknown fields
    assert_eq!(a.post("/api/v1/books", json!({"language_code": "en"})).await.status, StatusCode::UNPROCESSABLE_ENTITY);
    assert_eq!(a.post("/api/v1/books", json!({"title": 5, "language_code": "en"})).await.status, StatusCode::UNPROCESSABLE_ENTITY);
    assert_eq!(
        a.post("/api/v1/books", json!({"title": "x", "language_code": "en", "foo": 1})).await.status,
        StatusCode::UNPROCESSABLE_ENTITY
    );
    // 400: malformed JSON
    let r = a.req(Method::POST, "/api/v1/books", None, &[("x-user-id", "1".into()), ("content-type", "application/json".into())]).await;
    assert_eq!(r.status, StatusCode::BAD_REQUEST);
    // 400: bad path id, bad params
    assert_eq!(a.get("/api/v1/books/abc").await.status, StatusCode::BAD_REQUEST);
    assert_eq!(a.get("/api/v1/books/-5").await.status, StatusCode::BAD_REQUEST);
    for q in ["limit=0", "limit=101", "sort=isbn", "cursor=@@@", "year_from=abc", "title=ab", "offset=10", "language=xx1"] {
        let r = a.get(&format!("/api/v1/books?{q}")).await;
        assert_eq!(r.status, StatusCode::BAD_REQUEST, "{q}: {}", r.raw);
        assert_eq!(r.body["status"], 400);
    }
    for q in ["sort=title", "country=RUS", "born_from=x", "name=ab"] {
        assert_eq!(a.get(&format!("/api/v1/authors?{q}")).await.status, StatusCode::BAD_REQUEST, "{q}");
    }
    assert_eq!(a.get("/api/v1/search").await.status, StatusCode::BAD_REQUEST);
    assert_eq!(a.get("/api/v1/search?q=abc&type=films").await.status, StatusCode::BAD_REQUEST);
    // cursor from one sort used with another -> 400
    let r = a.get("/api/v1/books?sort=title&limit=1").await;
    if let Some(c) = r.body["next_cursor"].as_str() {
        assert_eq!(a.get(&format!("/api/v1/books?sort=-title&cursor={c}")).await.status, StatusCode::BAD_REQUEST);
        assert_eq!(a.get(&format!("/api/v1/authors?cursor={c}")).await.status, StatusCode::BAD_REQUEST);
    }
    // 404s
    assert_eq!(a.get("/api/v1/books/999999999").await.status, StatusCode::NOT_FOUND);
    assert_eq!(a.get("/api/v1/authors/999999999").await.status, StatusCode::NOT_FOUND);
    assert_eq!(a.get("/api/v1/authors/999999999/books").await.status, StatusCode::NOT_FOUND);
    assert_eq!(a.patch("/api/v1/books/999999999", json!({"title": "x"}), Some(1)).await.status, StatusCode::NOT_FOUND);
    assert_eq!(a.delete("/api/v1/books/999999999").await.status, StatusCode::NOT_FOUND);
}

// ---------------------------------------------------------------------------
// keyset pagination
// ---------------------------------------------------------------------------

async fn seed_sorting_fixture(a: &App, tag: &str) -> Vec<Value> {
    // duplicates in titles and years (incl. NULL years) to exercise id tie-breaks
    let titles = ["Alpha", "beta", "Gamma", "alpha", "Ärger", "Яблоко", "東京", "Alpha", "Zeta", "Alpha"];
    let mut out = Vec::new();
    for i in 0..37 {
        let year = if i % 6 == 0 { Value::Null } else { json!(1990 + (i % 4)) };
        let b = a
            .create_book(json!({"title": format!("{} {}", titles[i % titles.len()], if i % 3 == 0 { "" } else { "X" }),
                "language_code": "en", "publisher": tag, "publication_year": year}))
            .await;
        out.push(b);
    }
    out
}

#[tokio::test]
async fn keyset_pagination_every_sort_no_dupes_no_gaps() {
    let a = app().await;
    let tag = format!("pub-{}", token());
    let created = seed_sorting_fixture(&a, &tag).await;
    let all_ids: HashSet<i64> = ids(&created).into_iter().collect();
    for sort in ["title", "-title", "publication_year", "-publication_year", "created_at", "-created_at"] {
        for limit in [1, 5, 36, 100] {
            let items = a.collect_pages(&format!("/api/v1/books?publisher={tag}&sort={sort}&limit={limit}")).await;
            let got = ids(&items);
            let uniq: HashSet<i64> = got.iter().copied().collect();
            assert_eq!(got.len(), 37, "{sort}/{limit}: duplicates or gaps");
            assert_eq!(uniq, all_ids, "{sort}/{limit}");
            // order is consistent with the requested sort, id as tie-breaker
            let desc = sort.starts_with('-');
            let key = sort.trim_start_matches('-');
            for w in items.windows(2) {
                let (x, y) = (&w[0], &w[1]);
                let ord = match key {
                    "publication_year" => {
                        let f = |v: &Value| v["publication_year"].as_i64().unwrap_or(32767);
                        f(x).cmp(&f(y))
                    }
                    "created_at" => x["created_at"].as_str().cmp(&y["created_at"].as_str()),
                    _ => std::cmp::Ordering::Equal, // collation-dependent; checked via DB below
                }
                .then(id(x).cmp(&id(y)));
                if key != "title" {
                    assert!(if desc { ord.is_gt() } else { ord.is_lt() }, "{sort}: {x} vs {y}");
                }
            }
            if key == "title" {
                // compare with the database's own ordering (same collation as the index)
                let dir = if desc { "DESC" } else { "ASC" };
                let expected: Vec<(i64,)> = sqlx::query_as(&format!(
                    "SELECT id FROM books WHERE publisher = $1 AND deleted_at IS NULL ORDER BY title {dir}, id {dir}"
                ))
                .bind(&tag)
                .fetch_all(&a.pool)
                .await
                .unwrap();
                assert_eq!(got, expected.into_iter().map(|r| r.0).collect::<Vec<_>>(), "{sort}");
            }
        }
    }
    // stability: inserting a row "before" the cursor does not shift later pages
    let r = a.get(&format!("/api/v1/books?publisher={tag}&sort=title&limit=10")).await;
    let first: Vec<i64> = ids(r.body["items"].as_array().unwrap());
    let c = r.body["next_cursor"].as_str().unwrap().to_string();
    a.create_book(json!({"title": "AAAA first", "language_code": "en", "publisher": tag})).await;
    let rest = a.collect_pages(&format!("/api/v1/books?publisher={tag}&sort=title&limit=10&cursor={c}")).await;
    let rest_ids: HashSet<i64> = ids(&rest).into_iter().collect();
    assert_eq!(rest.len(), 27);
    assert!(first.iter().all(|i| !rest_ids.contains(i)));

    // estimate
    let r = a.get(&format!("/api/v1/books?publisher={tag}&with_total=true&limit=1")).await;
    assert_eq!(r.body["total_is_estimate"], true);
    assert!(r.body["total"].as_i64().unwrap() >= 1);
    assert_eq!(r.body["limit"], 1);
    let r = a.get("/api/v1/books?with_total=true&limit=1").await;
    assert_eq!(r.body["total_is_estimate"], true);
    assert!(r.body["total"].is_i64());
    let r = a.get(&format!("/api/v1/books?publisher={tag}&limit=1")).await;
    assert!(r.body["total"].is_null());
    assert_eq!(r.body["total_is_estimate"], false);
}

// ---------------------------------------------------------------------------
// filters
// ---------------------------------------------------------------------------

#[tokio::test]
async fn every_book_filter() {
    let a = app().await;
    let t = token();
    let tag = format!("pub-{t}");
    let creator = a.create_user().await;
    let au = id(&a.create_author(json!({"full_name": format!("Haruki Murakami{t}"), "original_name": "村上春樹"})).await);
    let other = id(&a.create_author(json!({"full_name": format!("Someone Else{t}")})).await);
    let isbn = random_isbn();
    let b1 = id(&a
        .create_book(json!({"title": format!("Norwegian Wood {t}"), "language_code": "ja", "publisher": tag,
        "publication_year": 1987, "genres": ["novel", "romance"], "isbn_13": isbn,
        "authors": [{"author_id": au.to_string()}]}))
        .await);
    let r = a
        .post_as(
            "/api/v1/books",
            json!({"title": format!("Kafka on the Shore {t}"), "language_code": "en", "publisher": tag,
        "publication_year": 2002, "genres": ["novel", "fantasy"], "subtitle": "Café édition",
        "authors": [{"author_id": au.to_string()}, {"author_id": other.to_string(), "role": "translator"}]}),
            creator,
        )
        .await;
    assert_eq!(r.status, StatusCode::CREATED, "{}", r.raw);
    let b2 = id(&r.body);
    let b3 = id(&a
        .create_book(json!({"title": format!("Unrelated {t}"), "language_code": "en", "publisher": tag,
        "genres": ["poetry"]}))
        .await);

    let check = |q: String, expected: Vec<i64>| {
        let a = &a;
        async move {
            let items = a.collect_pages(&format!("/api/v1/books?{q}&limit=2")).await;
            let mut got = ids(&items);
            got.sort_unstable();
            let mut exp = expected;
            exp.sort_unstable();
            assert_eq!(got, exp, "filter {q}");
        }
    };
    let p = format!("publisher={tag}");
    check(p.clone(), vec![b1, b2, b3]).await;
    check(format!("title=norwegian wood {t}"), vec![b1]).await;
    check(format!("title={}", &t[..5]), vec![b1, b2, b3]).await; // substring (trigram)
    check(format!("{p}&title=KAFKA"), vec![b2]).await; // case-insensitive
    check(format!("author=murakami{t}"), vec![b1, b2]).await;
    check(format!("author=村上春樹&{p}"), vec![b1, b2]).await; // original script
    check(format!("author_id={other}"), vec![b2]).await;
    check(format!("isbn={isbn}"), vec![b1]).await;
    check(format!("{p}&language=JA"), vec![b1]).await;
    check(format!("{p}&year_from=1990"), vec![b2]).await;
    check(format!("{p}&year_to=1990"), vec![b1]).await;
    check(format!("{p}&year_from=1987&year_to=2002"), vec![b1, b2]).await;
    check(format!("{p}&genre=Novel"), vec![b1, b2]).await;
    check(format!("{p}&genre=poetry"), vec![b3]).await;
    check(format!("{p}&created_by={creator}"), vec![b2]).await;
    check(format!("created_by={creator}"), vec![b2]).await;
    check(format!("{p}&created_from=2000-01-01"), vec![b1, b2, b3]).await;
    check(format!("{p}&created_to=2000-01-01"), vec![]).await;
    let now = (chrono::Utc::now() + chrono::Duration::minutes(1)).to_rfc3339_opts(chrono::SecondsFormat::Secs, true).replace('+', "%2B");
    check(format!("{p}&created_from={now}&created_to=2999-01-01"), vec![]).await;
    check(format!("q=wood {t}"), vec![b1]).await; // full-text, AND semantics
    check(format!("q=cafe {t}"), vec![b2]).await; // unaccent, subtitle indexed
    check(format!("q=\"shore {t}\" OR \"wood {t}\""), vec![b1, b2]).await; // websearch syntax
    check(format!("{p}&q=-unrelated {t}"), vec![b1, b2]).await;
    check(format!("{p}&genre=novel&language=en&year_from=2000&q=shore"), vec![b2]).await; // combined
}

#[tokio::test]
async fn author_list_filters_sorts_and_books() {
    let a = app().await;
    let t = token();
    let mk = |name: String, country: &str, born: Option<&str>| json!({"full_name": name, "country_code": country, "birth_date": born});
    let a1 = id(&a.create_author(mk(format!("Émile Zola{t}"), "FR", Some("1840-04-02"))).await);
    let a2 = id(&a.create_author(mk(format!("Anna Akhmatova{t}"), "RU", Some("1889-06-23"))).await);
    let a3 = id(&a.create_author(mk(format!("Anon Ymous{t}"), "RU", None)).await);
    let a4 = id(&a.create_author(mk(format!("Anna Karenina{t}"), "RU", Some("1889-06-23"))).await);

    let get_ids = |q: String| {
        let a = &a;
        async move { ids(&a.collect_pages(&format!("/api/v1/authors?{q}&limit=1")).await) }
    };
    let mut v = get_ids(format!("name={t}")).await;
    v.sort_unstable();
    let mut all = vec![a1, a2, a3, a4];
    all.sort_unstable();
    assert_eq!(v, all);
    assert_eq!(get_ids(format!("name=emile zola{t}")).await, vec![a1]); // unaccent
    let mut v = get_ids(format!("name={t}&country=ru")).await;
    v.sort_unstable();
    let mut exp = vec![a2, a3, a4];
    exp.sort_unstable();
    assert_eq!(v, exp);
    assert_eq!(get_ids(format!("name={t}&born_from=1850-01-01&born_to=1900-01-01&sort=birth_date")).await, {
        let mut e = vec![a2, a4];
        e.sort_unstable();
        e
    });
    assert_eq!(get_ids(format!("name={t}&born_to=1850-01-01")).await, vec![a1]);
    assert_eq!(get_ids(format!("q=akhmatova{t}")).await, vec![a2]);
    // sorts with ties & NULLs, compared against the database ordering
    for (sort, order) in [
        ("full_name", "full_name ASC, id ASC"),
        ("-full_name", "full_name DESC, id DESC"),
        ("birth_date", "coalesce(birth_date, 'infinity') ASC, id ASC"),
        ("-birth_date", "coalesce(birth_date, 'infinity') DESC, id DESC"),
        ("created_at", "created_at ASC, id ASC"),
        ("-created_at", "created_at DESC, id DESC"),
    ] {
        let got = get_ids(format!("name={t}&sort={sort}")).await;
        let exp: Vec<(i64,)> =
            sqlx::query_as(&format!("SELECT id FROM authors WHERE full_name LIKE $1 AND deleted_at IS NULL ORDER BY {order}"))
                .bind(format!("%{t}"))
                .fetch_all(&a.pool)
                .await
                .unwrap();
        assert_eq!(got, exp.into_iter().map(|r| r.0).collect::<Vec<_>>(), "{sort}");
    }
    let r = a.get(&format!("/api/v1/authors?name={t}&with_total=true")).await;
    assert_eq!(r.body["total_is_estimate"], true);
    let r = a.get("/api/v1/authors?with_total=true&limit=1").await;
    assert!(r.body["total"].is_i64());

    // books of an author, paginated by book id
    let mut book_ids = Vec::new();
    for i in 0..7 {
        let b = a
            .create_book(json!({"title": format!("Book {i} {t}"), "language_code": "fr",
            "authors": [{"author_id": a1.to_string()}, {"author_id": a1.to_string(), "role": "editor"}]}))
            .await;
        book_ids.push(id(&b));
    }
    let items = a.collect_pages(&format!("/api/v1/authors/{a1}/books?limit=3")).await;
    assert_eq!(ids(&items), book_ids);
    assert_eq!(items[0]["authors"].as_array().unwrap().len(), 2);
    assert_eq!(a.get(&format!("/api/v1/authors/{a1}/books?sort=title")).await.status, StatusCode::BAD_REQUEST);
    // deleting a book hides it from the author's list
    assert_eq!(a.delete(&format!("/api/v1/books/{}", book_ids[0])).await.status, StatusCode::NO_CONTENT);
    let items = a.collect_pages(&format!("/api/v1/authors/{a1}/books?limit=3")).await;
    assert_eq!(items.len(), 6);
}

// ---------------------------------------------------------------------------
// search
// ---------------------------------------------------------------------------

#[tokio::test]
async fn search_fulltext_fuzzy_and_paging() {
    let a = app().await;
    let t = token();
    let au = id(&a.create_author(json!({"full_name": format!("Fyodor Dostoevsky {t}"), "original_name": "Фёдор Достоевский"})).await);
    let b1 = id(&a
        .create_book(json!({"title": format!("Crime and Punishment {t}"), "language_code": "en",
        "authors": [{"author_id": au.to_string()}]}))
        .await);
    let b2 = id(&a
        .create_book(json!({"title": format!("{t} Brothers Karamazov"), "original_title": "Братья Карамазовы", "language_code": "en"}))
        .await);

    // full text across types
    let r = a.get(&format!("/api/v1/search?q={t}&type=all")).await;
    assert_eq!(r.status, StatusCode::OK, "{}", r.raw);
    let items = r.body["items"].as_array().unwrap();
    let got: HashSet<(String, i64)> = items.iter().map(|i| (i["type"].as_str().unwrap().to_string(), id(i))).collect();
    assert!(got.contains(&("book".into(), b1)) && got.contains(&("book".into(), b2)) && got.contains(&("author".into(), au)), "{}", r.raw);
    let ranks: Vec<f64> = items.iter().map(|i| i["rank"].as_f64().unwrap()).collect();
    assert!(ranks.windows(2).all(|w| w[0] >= w[1]), "sorted by rank desc");

    // type filters
    let r = a.get(&format!("/api/v1/search?q={t}&type=authors")).await;
    assert!(r.body["items"].as_array().unwrap().iter().all(|i| i["type"] == "author"));
    let r = a.get(&format!("/api/v1/search?q={t}&type=books&with_total=true")).await;
    assert!(r.body["items"].as_array().unwrap().iter().all(|i| i["type"] == "book"));
    assert_eq!(r.body["total_is_estimate"], true);

    // fuzzy: a typo (one letter dropped) still finds the token via trigram word similarity
    let typo: String = t.chars().enumerate().filter(|(i, _)| *i != 4).map(|(_, c)| c).collect();
    let r = a.get(&format!("/api/v1/search?q={typo}&type=books")).await;
    let got: HashSet<i64> = ids(r.body["items"].as_array().unwrap()).into_iter().collect();
    assert!(got.contains(&b1) && got.contains(&b2), "fuzzy: {}", r.raw);
    // non-latin script + unaccent
    let r = a.get("/api/v1/search?q=Достоевский&type=authors&limit=100").await;
    assert!(ids(r.body["items"].as_array().unwrap()).contains(&au));
    let r = a.get(&format!("/api/v1/search?q=brothers karamazov {t}")).await;
    assert_eq!(id(&r.body["items"][0]), b2);

    // keyset over (rank, type, id): no dupes across pages
    let paged = a.collect_pages(&format!("/api/v1/search?q={t}&limit=1")).await;
    let keys: Vec<(String, i64)> = paged.iter().map(|i| (i["type"].as_str().unwrap().to_string(), id(i))).collect();
    let uniq: HashSet<&(String, i64)> = keys.iter().collect();
    assert_eq!(keys.len(), uniq.len());
    assert_eq!(keys.len(), 3);
    // deleted entities disappear from search
    assert_eq!(a.delete(&format!("/api/v1/books/{b1}")).await.status, StatusCode::NO_CONTENT);
    let r = a.get(&format!("/api/v1/search?q={t}&type=books")).await;
    assert_eq!(ids(r.body["items"].as_array().unwrap()), vec![b2]);
}

#[tokio::test]
async fn link_tables_are_consistent() {
    let a = app().await;
    let t = token();
    let x = id(&a.create_author(json!({"full_name": format!("X {t}")})).await);
    let y = id(&a.create_author(json!({"full_name": format!("Y {t}")})).await);
    let b = a
        .create_book(json!({"title": format!("Linked {t}"), "language_code": "en",
        "authors": [{"author_id": x.to_string()}, {"author_id": y.to_string(), "role": "illustrator", "ordinal": 5}]}))
        .await;
    let bid = id(&b);
    let r =
        a.patch(&format!("/api/v1/books/{bid}"), json!({"authors": [{"author_id": y.to_string(), "role": "co-author"}]}), Some(1)).await;
    assert_eq!(r.status, StatusCode::OK, "{}", r.raw);
    let (n,): (i64,) = sqlx::query_as(
        "SELECT count(*) FROM (SELECT book_id, author_id, role, ordinal FROM book_authors WHERE book_id = $1 \
         EXCEPT SELECT book_id, author_id, role, ordinal FROM author_books WHERE author_id = ANY($2)) d",
    )
    .bind(bid)
    .bind(vec![x, y])
    .fetch_one(&a.pool)
    .await
    .unwrap();
    assert_eq!(n, 0);
    let (m,): (i64,) = sqlx::query_as("SELECT count(*) FROM author_books WHERE book_id = $1").bind(bid).fetch_one(&a.pool).await.unwrap();
    assert_eq!(m, 1);
    // a deleted author disappears from book cards
    assert_eq!(a.delete(&format!("/api/v1/authors/{y}")).await.status, StatusCode::NO_CONTENT);
    let r = a.get(&format!("/api/v1/books/{bid}")).await;
    assert!(r.body["authors"].as_array().unwrap().is_empty());
}

#[tokio::test]
async fn statement_timeout_maps_to_503() {
    // a pool with a tiny statement_timeout and a deliberately slow query
    let url = common::database_url();
    let pool = sqlx::postgres::PgPoolOptions::new().max_connections(1).connect(&url).await.unwrap();
    let db = world_library::db::Db::from_pool(pool, std::time::Duration::from_millis(50));
    let mut tx = db.begin().await.unwrap();
    let err = sqlx::query("SELECT pg_sleep(1)").execute(&mut *tx).await.unwrap_err();
    let app_err = world_library::error::AppError::from(err);
    assert_eq!(app_err.status(), StatusCode::SERVICE_UNAVAILABLE);
    assert!(matches!(app_err, world_library::error::AppError::Timeout));
}
