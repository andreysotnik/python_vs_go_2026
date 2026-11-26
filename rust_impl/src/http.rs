//! HTTP layer: router, extractors, middleware and thin handlers that delegate to
//! the service layer.

use std::{collections::HashMap, sync::Arc, time::Instant};

use axum::{
    Json, Router,
    extract::{FromRequest, FromRequestParts, MatchedPath, Path, Query, Request, State, rejection::JsonRejection},
    http::{HeaderMap, HeaderValue, StatusCode, header, request::Parts},
    middleware::{self, Next},
    response::{Html, IntoResponse, Response},
    routing::get,
};
use serde::de::DeserializeOwned;
use tower_http::{catch_panic::CatchPanicLayer, limit::RequestBodyLimitLayer, timeout::TimeoutLayer};

use crate::{
    error::{AppError, AppResult, Problem, problem_response},
    ids,
    metrics::Metrics,
    models::{AuthorInput, AuthorPatch, BookInput, BookPatch, UserInput},
    query::{AuthorListQuery, BookListQuery, Params, SearchQuery, parse_simple_page},
    service::{AUTHOR_BOOKS_KEY, Service, history_key},
};

pub const OPENAPI_YAML: &str = include_str!("../openapi.yaml");
pub const USER_HEADER: &str = "x-user-id";
pub const REQUEST_ID_HEADER: &str = "x-request-id";

#[derive(Clone)]
pub struct AppState {
    pub svc: Service,
    pub metrics: Arc<Metrics>,
    pub default_limit: u32,
}

// ---------------------------------------------------------------------------
// authentication (replaceable: a JWT middleware would insert the same Identity)
// ---------------------------------------------------------------------------

/// Authenticated caller, if any. Inserted into request extensions by `authenticate`.
#[derive(Debug, Clone, Copy)]
pub struct Identity(pub Option<i64>);

/// Simplified authentication: trusts the `X-User-Id` header.
pub async fn authenticate(mut req: Request, next: Next) -> Response {
    let identity = match req.headers().get(USER_HEADER) {
        None => Identity(None),
        Some(v) => match v.to_str().ok().and_then(ids::parse_id) {
            Some(id) => Identity(Some(id)),
            None => return AppError::Unauthorized("X-User-Id must be a positive integer user id".into()).into_response(),
        },
    };
    req.extensions_mut().insert(identity);
    next.run(req).await
}

/// Extractor for endpoints that require an authenticated actor (all writes).
pub struct Actor(pub i64);

impl<S: Send + Sync> FromRequestParts<S> for Actor {
    type Rejection = AppError;

    async fn from_request_parts(parts: &mut Parts, _: &S) -> Result<Self, Self::Rejection> {
        match parts.extensions.get::<Identity>() {
            Some(Identity(Some(id))) => Ok(Actor(*id)),
            _ => Err(AppError::Unauthorized("X-User-Id header is required for write operations".into())),
        }
    }
}

// ---------------------------------------------------------------------------
// extractors with RFC 7807 rejections
// ---------------------------------------------------------------------------

pub struct ApiJson<T>(pub T);

impl<S: Send + Sync, T: DeserializeOwned> FromRequest<S> for ApiJson<T> {
    type Rejection = AppError;

    async fn from_request(req: Request, state: &S) -> Result<Self, Self::Rejection> {
        match Json::<T>::from_request(req, state).await {
            Ok(Json(v)) => Ok(ApiJson(v)),
            Err(JsonRejection::JsonDataError(e)) => Err(AppError::Validation(vec![crate::error::FieldError::new(
                "body",
                e.body_text().replace("Failed to deserialize the JSON body into the target type: ", ""),
            )])),
            Err(JsonRejection::JsonSyntaxError(e)) => Err(AppError::bad_request(format!("malformed JSON: {}", e.body_text()))),
            Err(JsonRejection::MissingJsonContentType(_)) => Err(AppError::bad_request("expected Content-Type: application/json")),
            Err(e) => Err(AppError::bad_request(e.body_text())),
        }
    }
}

pub struct ApiQuery(pub Params);

impl<S: Send + Sync> FromRequestParts<S> for ApiQuery {
    type Rejection = AppError;

    async fn from_request_parts(parts: &mut Parts, state: &S) -> Result<Self, Self::Rejection> {
        let Query(map) = Query::<HashMap<String, String>>::from_request_parts(parts, state)
            .await
            .map_err(|e| AppError::bad_request(format!("malformed query string: {}", e.body_text())))?;
        Ok(ApiQuery(Params::new(map)))
    }
}

/// Positive numeric id from the path.
pub struct PathId(pub i64);

impl<S: Send + Sync> FromRequestParts<S> for PathId {
    type Rejection = AppError;

    async fn from_request_parts(parts: &mut Parts, state: &S) -> Result<Self, Self::Rejection> {
        let Path(raw) = Path::<String>::from_request_parts(parts, state).await.map_err(|e| AppError::bad_request(e.body_text()))?;
        ids::parse_id(&raw).map(PathId).ok_or_else(|| AppError::bad_request(format!("invalid id '{raw}': must be a positive integer")))
    }
}

/// Parses `If-Match: "3"` / `W/"3"` / `3` into a version number.
pub fn if_match(headers: &HeaderMap) -> AppResult<Option<i32>> {
    let Some(v) = headers.get(header::IF_MATCH) else { return Ok(None) };
    let s = v.to_str().unwrap_or_default().trim();
    let s = s.strip_prefix("W/").unwrap_or(s).trim_matches('"');
    s.parse::<i32>().map(Some).map_err(|_| AppError::bad_request("If-Match must contain the entity version, e.g. If-Match: \"3\""))
}

fn with_etag(version: i32, body: impl IntoResponse) -> Response {
    let mut r = body.into_response();
    if let Ok(v) = HeaderValue::from_str(&format!("\"{version}\"")) {
        r.headers_mut().insert(header::ETAG, v);
    }
    r
}

fn created(location: String, version: i32, body: impl IntoResponse) -> Response {
    let mut r = with_etag(version, (StatusCode::CREATED, body));
    if let Ok(v) = HeaderValue::from_str(&location) {
        r.headers_mut().insert(header::LOCATION, v);
    }
    r
}

// ---------------------------------------------------------------------------
// handlers: books
// ---------------------------------------------------------------------------

async fn list_books(State(st): State<AppState>, ApiQuery(p): ApiQuery) -> AppResult<Response> {
    let q = BookListQuery::parse(&p, st.default_limit)?;
    Ok(Json(st.svc.list_books(q).await?).into_response())
}

async fn get_book(State(st): State<AppState>, PathId(id): PathId) -> AppResult<Response> {
    let b = st.svc.get_book(id).await?;
    Ok(with_etag(b.version, Json(b)))
}

async fn create_book(State(st): State<AppState>, Actor(user): Actor, ApiJson(body): ApiJson<BookInput>) -> AppResult<Response> {
    let b = st.svc.create_book(body, user).await?;
    Ok(created(format!("/api/v1/books/{}", b.id), b.version, Json(b)))
}

async fn put_book(
    State(st): State<AppState>,
    Actor(user): Actor,
    PathId(id): PathId,
    headers: HeaderMap,
    ApiJson(body): ApiJson<BookInput>,
) -> AppResult<Response> {
    let b = st.svc.replace_book(id, body, user, if_match(&headers)?).await?;
    Ok(with_etag(b.version, Json(b)))
}

async fn patch_book(
    State(st): State<AppState>,
    Actor(user): Actor,
    PathId(id): PathId,
    headers: HeaderMap,
    ApiJson(body): ApiJson<BookPatch>,
) -> AppResult<Response> {
    let b = st.svc.patch_book(id, body, user, if_match(&headers)?).await?;
    Ok(with_etag(b.version, Json(b)))
}

async fn delete_book(State(st): State<AppState>, Actor(user): Actor, PathId(id): PathId, headers: HeaderMap) -> AppResult<StatusCode> {
    st.svc.delete_book(id, user, if_match(&headers)?).await?;
    Ok(StatusCode::NO_CONTENT)
}

async fn book_history(State(st): State<AppState>, PathId(id): PathId, ApiQuery(p): ApiQuery) -> AppResult<Response> {
    let page = parse_simple_page(&p, &history_key("book"), false, st.default_limit)?;
    Ok(Json(st.svc.history("book", id, page).await?).into_response())
}

// ---------------------------------------------------------------------------
// handlers: authors
// ---------------------------------------------------------------------------

async fn list_authors(State(st): State<AppState>, ApiQuery(p): ApiQuery) -> AppResult<Response> {
    let q = AuthorListQuery::parse(&p, st.default_limit)?;
    Ok(Json(st.svc.list_authors(q).await?).into_response())
}

async fn get_author(State(st): State<AppState>, PathId(id): PathId) -> AppResult<Response> {
    let a = st.svc.get_author(id).await?;
    Ok(with_etag(a.version, Json(a)))
}

async fn author_books(State(st): State<AppState>, PathId(id): PathId, ApiQuery(p): ApiQuery) -> AppResult<Response> {
    let page = parse_simple_page(&p, AUTHOR_BOOKS_KEY, false, st.default_limit)?;
    Ok(Json(st.svc.books_of_author(id, page).await?).into_response())
}

async fn author_history(State(st): State<AppState>, PathId(id): PathId, ApiQuery(p): ApiQuery) -> AppResult<Response> {
    let page = parse_simple_page(&p, &history_key("author"), false, st.default_limit)?;
    Ok(Json(st.svc.history("author", id, page).await?).into_response())
}

async fn create_author(State(st): State<AppState>, Actor(user): Actor, ApiJson(body): ApiJson<AuthorInput>) -> AppResult<Response> {
    let a = st.svc.create_author(body, user).await?;
    Ok(created(format!("/api/v1/authors/{}", a.id), a.version, Json(a)))
}

async fn put_author(
    State(st): State<AppState>,
    Actor(user): Actor,
    PathId(id): PathId,
    headers: HeaderMap,
    ApiJson(body): ApiJson<AuthorInput>,
) -> AppResult<Response> {
    let a = st.svc.replace_author(id, body, user, if_match(&headers)?).await?;
    Ok(with_etag(a.version, Json(a)))
}

async fn patch_author(
    State(st): State<AppState>,
    Actor(user): Actor,
    PathId(id): PathId,
    headers: HeaderMap,
    ApiJson(body): ApiJson<AuthorPatch>,
) -> AppResult<Response> {
    let a = st.svc.patch_author(id, body, user, if_match(&headers)?).await?;
    Ok(with_etag(a.version, Json(a)))
}

async fn delete_author(State(st): State<AppState>, Actor(user): Actor, PathId(id): PathId, headers: HeaderMap) -> AppResult<StatusCode> {
    st.svc.delete_author(id, user, if_match(&headers)?).await?;
    Ok(StatusCode::NO_CONTENT)
}

// ---------------------------------------------------------------------------
// handlers: search, users, system
// ---------------------------------------------------------------------------

async fn search(State(st): State<AppState>, ApiQuery(p): ApiQuery) -> AppResult<Response> {
    let q = SearchQuery::parse(&p, st.default_limit)?;
    Ok(Json(st.svc.search(q).await?).into_response())
}

async fn create_user(
    State(st): State<AppState>,
    identity: Option<axum::Extension<Identity>>,
    ApiJson(body): ApiJson<UserInput>,
) -> AppResult<Response> {
    let actor = identity.and_then(|axum::Extension(Identity(id))| id);
    let u = st.svc.create_user(body, actor).await?;
    let mut r = (StatusCode::CREATED, Json(&u)).into_response();
    if let Ok(v) = HeaderValue::from_str(&format!("/api/v1/users/{}", u.id)) {
        r.headers_mut().insert(header::LOCATION, v);
    }
    Ok(r)
}

async fn get_user(State(st): State<AppState>, PathId(id): PathId) -> AppResult<Response> {
    Ok(Json(st.svc.get_user(id).await?).into_response())
}

async fn healthz() -> Response {
    Json(serde_json::json!({"status": "ok"})).into_response()
}

async fn readyz(State(st): State<AppState>) -> Response {
    match st.svc.db.ping().await {
        Ok(()) => Json(serde_json::json!({"status": "ready"})).into_response(),
        Err(e) => {
            tracing::warn!(error = %e, "readiness check failed");
            AppError::Unavailable("database is not reachable".into()).into_response()
        }
    }
}

async fn metrics(State(st): State<AppState>) -> Response {
    st.metrics.db_pool_size.set(i64::from(st.svc.db.pool.size()));
    st.metrics.db_pool_idle.set(st.svc.db.pool.num_idle() as i64);
    ([(header::CONTENT_TYPE, "text/plain; version=0.0.4")], st.metrics.render()).into_response()
}

async fn openapi() -> Response {
    ([(header::CONTENT_TYPE, "application/yaml")], OPENAPI_YAML).into_response()
}

const SWAGGER_HTML: &str = r#"<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>World Library API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js" crossorigin></script>
  <script>
    window.onload = () => { window.ui = SwaggerUIBundle({ url: '/openapi.yaml', dom_id: '#swagger-ui' }); };
  </script>
</body>
</html>"#;

async fn docs() -> Html<&'static str> {
    Html(SWAGGER_HTML)
}

async fn not_found() -> Response {
    let p = Problem {
        r#type: "https://world-library.example/problems/route-not-found".into(),
        title: "Not Found".into(),
        status: 404,
        detail: "no such route; see /docs".into(),
        errors: vec![],
    };
    problem_response(StatusCode::NOT_FOUND, &p)
}

async fn method_not_allowed() -> Response {
    let p = Problem {
        r#type: "https://world-library.example/problems/method-not-allowed".into(),
        title: "Method Not Allowed".into(),
        status: 405,
        detail: "method not allowed for this route; see /docs".into(),
        errors: vec![],
    };
    problem_response(StatusCode::METHOD_NOT_ALLOWED, &p)
}

// ---------------------------------------------------------------------------
// observability middleware: request id, structured access log, metrics
// ---------------------------------------------------------------------------

async fn observe(State(st): State<AppState>, req: Request, next: Next) -> Response {
    let start = Instant::now();
    let method = req.method().to_string();
    let path = req.uri().path().to_string();
    let route = req.extensions().get::<MatchedPath>().map_or_else(|| "unmatched".to_string(), |m| m.as_str().to_string());
    let request_id = req
        .headers()
        .get(REQUEST_ID_HEADER)
        .and_then(|v| v.to_str().ok())
        .filter(|s| !s.is_empty() && s.len() <= 128)
        .map_or_else(|| format!("{:x}", st.svc.ids.next_id()), str::to_string);
    let user = req.headers().get(USER_HEADER).and_then(|v| v.to_str().ok()).unwrap_or("").to_string();

    let mut resp = next.run(req).await;
    let status = resp.status().as_u16();
    let elapsed = start.elapsed();
    st.metrics.observe(&method, &route, status, elapsed.as_secs_f64());
    if let Ok(v) = HeaderValue::from_str(&request_id) {
        resp.headers_mut().insert(REQUEST_ID_HEADER, v);
    }
    if route != "/healthz" && route != "/metrics" {
        tracing::info!(
            target: "access",
            request_id = %request_id,
            method = %method,
            path = %path,
            route = %route,
            status,
            user_id = %user,
            latency_ms = elapsed.as_secs_f64() * 1000.0,
            "request"
        );
    }
    resp
}

pub fn router(state: AppState, request_timeout: std::time::Duration) -> Router {
    let api = Router::new()
        .route("/books", get(list_books).post(create_book))
        .route("/books/{id}", get(get_book).put(put_book).patch(patch_book).delete(delete_book))
        .route("/books/{id}/history", get(book_history))
        .route("/authors", get(list_authors).post(create_author))
        .route("/authors/{id}", get(get_author).put(put_author).patch(patch_author).delete(delete_author))
        .route("/authors/{id}/books", get(author_books))
        .route("/authors/{id}/history", get(author_history))
        .route("/search", get(search))
        .route("/users", axum::routing::post(create_user))
        .route("/users/{id}", get(get_user))
        .layer(middleware::from_fn(authenticate));

    Router::new()
        .nest("/api/v1", api)
        .route("/healthz", get(healthz))
        .route("/readyz", get(readyz))
        .route("/metrics", get(metrics))
        .route("/openapi.yaml", get(openapi))
        .route("/docs", get(docs))
        .fallback(not_found)
        .method_not_allowed_fallback(method_not_allowed)
        .layer(middleware::from_fn_with_state(state.clone(), observe))
        .layer(RequestBodyLimitLayer::new(1024 * 1024))
        .layer(TimeoutLayer::with_status_code(StatusCode::GATEWAY_TIMEOUT, request_timeout))
        .layer(CatchPanicLayer::new())
        .with_state(state)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn if_match_forms() {
        let mut h = HeaderMap::new();
        assert_eq!(if_match(&h).unwrap(), None);
        h.insert(header::IF_MATCH, HeaderValue::from_static("\"3\""));
        assert_eq!(if_match(&h).unwrap(), Some(3));
        h.insert(header::IF_MATCH, HeaderValue::from_static("W/\"4\""));
        assert_eq!(if_match(&h).unwrap(), Some(4));
        h.insert(header::IF_MATCH, HeaderValue::from_static("5"));
        assert_eq!(if_match(&h).unwrap(), Some(5));
        h.insert(header::IF_MATCH, HeaderValue::from_static("*"));
        assert!(if_match(&h).is_err());
    }
}
