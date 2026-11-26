//! Application error type rendered as RFC 7807 `application/problem+json`.

use axum::{
    http::{HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
};
use serde::Serialize;

/// A single field-level validation message.
#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct FieldError {
    pub field: String,
    pub message: String,
}

impl FieldError {
    pub fn new(field: impl Into<String>, message: impl Into<String>) -> Self {
        Self { field: field.into(), message: message.into() }
    }
}

#[derive(Debug, thiserror::Error)]
pub enum AppError {
    /// Malformed request: bad query parameter, unknown sort, invalid cursor, broken JSON.
    #[error("bad request: {0}")]
    BadRequest(String, Vec<FieldError>),
    /// Well-formed but semantically invalid body.
    #[error("validation failed")]
    Validation(Vec<FieldError>),
    #[error("unauthorized: {0}")]
    Unauthorized(String),
    #[error("not found: {0}")]
    NotFound(String),
    #[error("conflict: {0}")]
    Conflict(String),
    #[error("precondition required: {0}")]
    PreconditionRequired(String),
    #[error("database timeout")]
    Timeout,
    #[error("service unavailable: {0}")]
    Unavailable(String),
    #[error("internal error: {0}")]
    Internal(String),
}

pub type AppResult<T> = Result<T, AppError>;

impl AppError {
    pub fn bad_request(msg: impl Into<String>) -> Self {
        Self::BadRequest(msg.into(), Vec::new())
    }

    pub fn bad_param(field: &str, msg: impl Into<String>) -> Self {
        let msg = msg.into();
        Self::BadRequest(format!("invalid query parameter '{field}': {msg}"), vec![FieldError::new(field, msg)])
    }

    pub fn validation_one(field: &str, msg: impl Into<String>) -> Self {
        Self::Validation(vec![FieldError::new(field, msg)])
    }

    pub fn status(&self) -> StatusCode {
        match self {
            Self::BadRequest(..) => StatusCode::BAD_REQUEST,
            Self::Validation(_) => StatusCode::UNPROCESSABLE_ENTITY,
            Self::Unauthorized(_) => StatusCode::UNAUTHORIZED,
            Self::NotFound(_) => StatusCode::NOT_FOUND,
            Self::Conflict(_) => StatusCode::CONFLICT,
            Self::PreconditionRequired(_) => StatusCode::PRECONDITION_REQUIRED,
            Self::Timeout => StatusCode::SERVICE_UNAVAILABLE,
            Self::Unavailable(_) => StatusCode::SERVICE_UNAVAILABLE,
            Self::Internal(_) => StatusCode::INTERNAL_SERVER_ERROR,
        }
    }

    fn slug(&self) -> &'static str {
        match self {
            Self::BadRequest(..) => "bad-request",
            Self::Validation(_) => "validation-error",
            Self::Unauthorized(_) => "unauthorized",
            Self::NotFound(_) => "not-found",
            Self::Conflict(_) => "conflict",
            Self::PreconditionRequired(_) => "precondition-required",
            Self::Timeout => "query-timeout",
            Self::Unavailable(_) => "unavailable",
            Self::Internal(_) => "internal-error",
        }
    }

    pub fn to_problem(&self) -> Problem {
        let status = self.status();
        let (detail, errors) = match self {
            Self::BadRequest(m, e) => (m.clone(), e.clone()),
            Self::Validation(e) => ("request body failed validation".to_string(), e.clone()),
            Self::Unauthorized(m) | Self::NotFound(m) | Self::Conflict(m) | Self::PreconditionRequired(m) | Self::Unavailable(m) => {
                (m.clone(), Vec::new())
            }
            Self::Timeout => ("the query exceeded statement_timeout; narrow the filters".to_string(), Vec::new()),
            // Never leak internals to clients; details go to the log.
            Self::Internal(_) => ("unexpected server error".to_string(), Vec::new()),
        };
        Problem {
            r#type: format!("https://world-library.example/problems/{}", self.slug()),
            title: status.canonical_reason().unwrap_or("Error").to_string(),
            status: status.as_u16(),
            detail,
            errors,
        }
    }
}

/// RFC 7807 problem document.
#[derive(Debug, Serialize)]
pub struct Problem {
    #[serde(rename = "type")]
    pub r#type: String,
    pub title: String,
    pub status: u16,
    pub detail: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub errors: Vec<FieldError>,
}

pub fn problem_response(status: StatusCode, problem: &Problem) -> Response {
    let body = serde_json::to_vec(problem).unwrap_or_default();
    let mut resp = (status, body).into_response();
    resp.headers_mut().insert(header::CONTENT_TYPE, HeaderValue::from_static("application/problem+json"));
    resp
}

impl IntoResponse for AppError {
    fn into_response(self) -> Response {
        let status = self.status();
        if status.is_server_error() {
            tracing::error!(error = %self, "request failed");
        }
        problem_response(status, &self.to_problem())
    }
}

impl From<sqlx::Error> for AppError {
    fn from(e: sqlx::Error) -> Self {
        if let sqlx::Error::Database(db) = &e {
            let constraint = db.constraint().unwrap_or_default().to_string();
            match db.code().as_deref() {
                // query_canceled: statement_timeout fired
                Some("57014") => return Self::Timeout,
                Some("23505") => {
                    if constraint.starts_with("isbn_registry") {
                        return Self::Conflict("a book with this isbn_13 already exists".into());
                    }
                    if constraint.starts_with("users_username") {
                        return Self::Conflict("username already taken".into());
                    }
                    if constraint.starts_with("users_email") {
                        return Self::Conflict("email already registered".into());
                    }
                    return Self::Conflict(format!("duplicate value violates {constraint}"));
                }
                Some("23503") => {
                    if constraint.contains("created_by") || constraint.contains("updated_by") {
                        return Self::Unauthorized("X-User-Id refers to an unknown user".into());
                    }
                    return Self::Validation(vec![FieldError::new(constraint, "referenced entity does not exist")]);
                }
                Some("23514") | Some("22007") | Some("22008") | Some("22003") => {
                    return Self::Validation(vec![FieldError::new(
                        if constraint.is_empty() { "body".to_string() } else { constraint },
                        db.message().to_string(),
                    )]);
                }
                _ => {}
            }
        }
        match e {
            sqlx::Error::PoolTimedOut => Self::Unavailable("database connection pool exhausted".into()),
            other => Self::Internal(other.to_string()),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn statuses() {
        assert_eq!(AppError::bad_request("x").status(), StatusCode::BAD_REQUEST);
        assert_eq!(AppError::validation_one("a", "b").status(), StatusCode::UNPROCESSABLE_ENTITY);
        assert_eq!(AppError::NotFound("x".into()).status(), StatusCode::NOT_FOUND);
        assert_eq!(AppError::Conflict("x".into()).status(), StatusCode::CONFLICT);
        assert_eq!(AppError::PreconditionRequired("x".into()).status(), StatusCode::PRECONDITION_REQUIRED);
        assert_eq!(AppError::Unauthorized("x".into()).status(), StatusCode::UNAUTHORIZED);
        assert_eq!(AppError::Timeout.status(), StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(AppError::Unavailable("x".into()).status(), StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(AppError::Internal("x".into()).status(), StatusCode::INTERNAL_SERVER_ERROR);
    }

    #[test]
    fn problem_shape() {
        let p = AppError::bad_param("limit", "must be between 1 and 100").to_problem();
        let v = serde_json::to_value(&p).unwrap();
        assert_eq!(v["status"], 400);
        assert_eq!(v["type"], "https://world-library.example/problems/bad-request");
        assert_eq!(v["errors"][0]["field"], "limit");
        let p = AppError::Internal("secret".into()).to_problem();
        assert!(!p.detail.contains("secret"));
        let v = serde_json::to_value(AppError::NotFound("nope".into()).to_problem()).unwrap();
        assert!(v.get("errors").is_none());
    }

    #[test]
    fn pool_timeout_maps_to_503() {
        assert_eq!(AppError::from(sqlx::Error::PoolTimedOut).status(), StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(AppError::from(sqlx::Error::RowNotFound).status(), StatusCode::INTERNAL_SERVER_ERROR);
    }
}
