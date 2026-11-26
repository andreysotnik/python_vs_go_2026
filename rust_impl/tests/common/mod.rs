//! Integration-test harness: one fresh database per test binary run (created from
//! TEST_DATABASE_URL, migrated with the real migrations), one pool per test, requests
//! go through the real axum router (tower::oneshot, no network).

#![allow(dead_code)]

use std::{
    sync::{
        OnceLock,
        atomic::{AtomicU16, Ordering},
    },
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use axum::{
    Router,
    body::Body,
    http::{HeaderMap, Method, Request, StatusCode},
};
use http_body_util::BodyExt;
use rand::Rng;
use serde_json::{Value, json};
use sqlx::{Connection, Executor, PgConnection, postgres::PgPoolOptions};
use tower::ServiceExt;
use world_library::{build_state, db::Db, http::router};

static DB_URL: OnceLock<String> = OnceLock::new();
/// Every test app gets its own snowflake node id, exactly like separate app replicas.
static NODE: AtomicU16 = AtomicU16::new(10);

fn admin_url() -> String {
    std::env::var("TEST_DATABASE_URL").unwrap_or_else(|_| "postgres://library:library@127.0.0.1:35432/postgres".to_string())
}

fn with_db(url: &str, db: &str) -> String {
    let (base, query) = match url.split_once('?') {
        Some((b, q)) => (b, format!("?{q}")),
        None => (url, String::new()),
    };
    let base = base.rsplit_once('/').map_or(base, |(b, _)| b);
    format!("{base}/{db}{query}")
}

/// Creates (once per process) a fresh, migrated database and returns its URL.
pub fn database_url() -> String {
    DB_URL
        .get_or_init(|| {
            std::thread::spawn(|| {
                let rt = tokio::runtime::Builder::new_current_thread().enable_all().build().expect("runtime");
                rt.block_on(async {
                    let admin = admin_url();
                    let mut conn = PgConnection::connect(&admin).await.expect("connect TEST_DATABASE_URL (is postgres running? `make up`)");
                    let stale: Vec<(String,)> = sqlx::query_as("SELECT datname::text FROM pg_database WHERE datname LIKE 'wl_test_%'")
                        .fetch_all(&mut conn)
                        .await
                        .unwrap_or_default();
                    for (d,) in stale {
                        let _ = conn.execute(format!("DROP DATABASE IF EXISTS {d} WITH (FORCE)").as_str()).await;
                    }
                    let name = format!(
                        "wl_test_{}_{}",
                        std::process::id(),
                        SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_millis()).unwrap_or(0)
                    );
                    conn.execute(format!("CREATE DATABASE {name}").as_str()).await.expect("create test database");
                    let url = with_db(&admin, &name);
                    world_library::db::migrate(&url).await.expect("migrations");
                    url
                })
            })
            .join()
            .expect("db init thread")
        })
        .clone()
}

/// Creates a brand-new migrated database (for tests that need exclusive tables, e.g. seed).
pub async fn fresh_database(prefix: &str) -> String {
    let admin = admin_url();
    let mut conn = PgConnection::connect(&admin).await.expect("connect TEST_DATABASE_URL");
    let name = format!(
        "wl_test_{prefix}_{}_{}",
        std::process::id(),
        SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_millis()).unwrap_or(0)
    );
    conn.execute(format!("CREATE DATABASE {name}").as_str()).await.expect("create database");
    let url = with_db(&admin, &name);
    world_library::db::migrate(&url).await.expect("migrations");
    url
}

pub struct App {
    pub router: Router,
    pub pool: sqlx::PgPool,
    /// Default actor (the bootstrap `system` user).
    pub user: i64,
}

pub async fn app() -> App {
    let url = database_url();
    let pool = PgPoolOptions::new().max_connections(4).connect(&url).await.expect("pool");
    let db = Db::from_pool(pool.clone(), Duration::from_secs(10));
    let state = build_state(db, NODE.fetch_add(1, Ordering::Relaxed), 20).expect("state");
    App { router: router(state, Duration::from_secs(30)), pool, user: 1 }
}

pub struct Resp {
    pub status: StatusCode,
    pub headers: HeaderMap,
    pub body: Value,
    pub raw: String,
}

impl App {
    pub async fn req(&self, method: Method, uri: &str, body: Option<Value>, headers: &[(&str, String)]) -> Resp {
        let uri = encode_uri(uri);
        let mut b = Request::builder().method(method).uri(uri);
        for (k, v) in headers {
            b = b.header(*k, v);
        }
        let req = match body {
            Some(v) => b.header("content-type", "application/json").body(Body::from(v.to_string())).expect("request"),
            None => b.body(Body::empty()).expect("request"),
        };
        let resp = self.router.clone().oneshot(req).await.expect("response");
        let status = resp.status();
        let headers = resp.headers().clone();
        let bytes = resp.into_body().collect().await.expect("body").to_bytes();
        let raw = String::from_utf8_lossy(&bytes).to_string();
        let body = serde_json::from_slice(&bytes).unwrap_or(Value::Null);
        Resp { status, headers, body, raw }
    }

    pub async fn get(&self, uri: &str) -> Resp {
        self.req(Method::GET, uri, None, &[]).await
    }

    fn actor(&self) -> Vec<(&'static str, String)> {
        vec![("x-user-id", self.user.to_string())]
    }

    pub async fn post(&self, uri: &str, body: Value) -> Resp {
        self.req(Method::POST, uri, Some(body), &self.actor()).await
    }

    pub async fn post_as(&self, uri: &str, body: Value, user: i64) -> Resp {
        self.req(Method::POST, uri, Some(body), &[("x-user-id", user.to_string())]).await
    }

    pub async fn put(&self, uri: &str, body: Value, version: Option<i32>) -> Resp {
        let mut h = self.actor();
        if let Some(v) = version {
            h.push(("if-match", format!("\"{v}\"")));
        }
        self.req(Method::PUT, uri, Some(body), &h).await
    }

    pub async fn patch(&self, uri: &str, body: Value, version: Option<i32>) -> Resp {
        self.patch_as(uri, body, version, self.user).await
    }

    pub async fn patch_as(&self, uri: &str, body: Value, version: Option<i32>, user: i64) -> Resp {
        let mut h = vec![("x-user-id", user.to_string())];
        if let Some(v) = version {
            h.push(("if-match", format!("\"{v}\"")));
        }
        self.req(Method::PATCH, uri, Some(body), &h).await
    }

    pub async fn delete(&self, uri: &str) -> Resp {
        self.req(Method::DELETE, uri, None, &self.actor()).await
    }

    pub async fn create_user(&self) -> i64 {
        let t = token();
        let r = self
            .req(Method::POST, "/api/v1/users", Some(json!({"username": format!("u_{t}"), "email": format!("{t}@example.org")})), &[])
            .await;
        assert_eq!(r.status, StatusCode::CREATED, "{}", r.raw);
        id(&r.body)
    }

    pub async fn create_author(&self, body: Value) -> Value {
        let r = self.post("/api/v1/authors", body).await;
        assert_eq!(r.status, StatusCode::CREATED, "{}", r.raw);
        r.body
    }

    pub async fn create_book(&self, body: Value) -> Value {
        let r = self.post("/api/v1/books", body).await;
        assert_eq!(r.status, StatusCode::CREATED, "{}", r.raw);
        r.body
    }

    /// Follows next_cursor until the end; returns all items.
    pub async fn collect_pages(&self, base: &str) -> Vec<Value> {
        let mut out = Vec::new();
        let mut uri = base.to_string();
        for _ in 0..1000 {
            let r = self.get(&uri).await;
            assert_eq!(r.status, StatusCode::OK, "{uri}: {}", r.raw);
            out.extend(r.body["items"].as_array().cloned().unwrap_or_default());
            match r.body["next_cursor"].as_str() {
                Some(c) => {
                    let sep = if base.contains('?') { '&' } else { '?' };
                    uri = format!("{base}{sep}cursor={c}");
                }
                None => return out,
            }
        }
        panic!("pagination did not terminate");
    }
}

/// Percent-encodes spaces, quotes and non-ASCII characters (tests write readable URIs).
pub fn encode_uri(uri: &str) -> String {
    let mut out = String::with_capacity(uri.len());
    for c in uri.chars() {
        if c.is_ascii_alphanumeric() || "/?&=-_.~%:+,*".contains(c) {
            out.push(c);
        } else {
            let mut buf = [0u8; 4];
            for b in c.encode_utf8(&mut buf).bytes() {
                out.push_str(&format!("%{b:02X}"));
            }
        }
    }
    out
}

/// Random lowercase token, unique per call (isolates tests sharing one database).
pub fn token() -> String {
    let mut rng = rand::thread_rng();
    (0..10).map(|_| char::from(b'a' + rng.gen_range(0..26u8))).collect()
}

pub fn id(v: &Value) -> i64 {
    v["id"].as_str().and_then(|s| s.parse().ok()).unwrap_or_else(|| panic!("no id in {v}"))
}

pub fn ids(items: &[Value]) -> Vec<i64> {
    items.iter().map(id).collect()
}

/// A valid ISBN-13 that is unique with overwhelming probability.
pub fn random_isbn() -> String {
    let body: u64 = rand::thread_rng().gen_range(0..1_000_000_000);
    world_library::isbn::make_isbn13(979, body)
}
