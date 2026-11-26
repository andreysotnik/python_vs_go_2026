//! API DTOs, persistence rows and the editable "data" structs that validation and
//! diffing operate on.

use chrono::{DateTime, NaiveDate, Utc};
use serde::{Deserialize, Deserializer, Serialize};
use serde_json::Value;

use crate::ids::as_string;

/// Distinguishes "field absent" (`None`) from "explicit null" (`Some(None)`) in PATCH bodies.
pub fn double_option<'de, T, D>(d: D) -> Result<Option<Option<T>>, D::Error>
where
    T: Deserialize<'de>,
    D: Deserializer<'de>,
{
    Option::<T>::deserialize(d).map(Some)
}

pub const ROLES: [&str; 5] = ["author", "co-author", "translator", "editor", "illustrator"];

// ---------------------------------------------------------------------------
// shared
// ---------------------------------------------------------------------------

#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct UserRef {
    #[serde(with = "as_string")]
    pub id: i64,
    pub username: Option<String>,
}

#[derive(Debug, Serialize)]
pub struct ListResponse<T> {
    pub items: Vec<T>,
    pub next_cursor: Option<String>,
    pub limit: u32,
    pub total: Option<i64>,
    pub total_is_estimate: bool,
}

// ---------------------------------------------------------------------------
// users
// ---------------------------------------------------------------------------

#[derive(Debug, Clone, Serialize, sqlx::FromRow)]
pub struct User {
    #[serde(with = "as_string")]
    pub id: i64,
    pub username: String,
    pub email: String,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct UserInput {
    pub username: String,
    pub email: String,
}

// ---------------------------------------------------------------------------
// books
// ---------------------------------------------------------------------------

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct BookRow {
    pub id: i64,
    pub title: String,
    pub original_title: Option<String>,
    pub subtitle: Option<String>,
    pub description: Option<String>,
    pub isbn_10: Option<String>,
    pub isbn_13: Option<String>,
    pub language_code: String,
    pub publication_year: Option<i16>,
    pub publisher: Option<String>,
    pub page_count: Option<i32>,
    pub genres: Vec<String>,
    pub edition: Option<String>,
    pub external_ids: Value,
    pub created_at: DateTime<Utc>,
    pub created_by: i64,
    pub updated_at: DateTime<Utc>,
    pub updated_by: i64,
    pub deleted_at: Option<DateTime<Utc>>,
    pub version: i32,
}

/// A link of a book to an author as stored (book_authors / author_books).
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AuthorLink {
    #[serde(with = "as_string")]
    pub author_id: i64,
    #[serde(default = "default_role")]
    pub role: String,
    #[serde(default)]
    pub ordinal: Option<i16>,
}

fn default_role() -> String {
    "author".into()
}

/// Author reference embedded into a book card.
#[derive(Debug, Clone, Serialize, sqlx::FromRow)]
pub struct BookAuthor {
    #[serde(skip)]
    pub book_id: i64,
    #[serde(with = "as_string")]
    pub author_id: i64,
    pub full_name: String,
    pub role: String,
    pub ordinal: i16,
}

/// Editable state of a book. Create/PUT/PATCH all produce one of these, which is then
/// validated as a whole and diffed against the previous state for the audit log.
#[derive(Debug, Clone, PartialEq, Serialize)]
pub struct BookData {
    pub title: String,
    pub original_title: Option<String>,
    pub subtitle: Option<String>,
    pub description: Option<String>,
    pub isbn_10: Option<String>,
    pub isbn_13: Option<String>,
    pub language_code: String,
    pub publication_year: Option<i32>,
    pub publisher: Option<String>,
    pub page_count: Option<i32>,
    pub genres: Vec<String>,
    pub edition: Option<String>,
    pub external_ids: Value,
    pub authors: Vec<AuthorLink>,
}

/// Body of POST and PUT (full representation).
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct BookInput {
    pub title: String,
    pub original_title: Option<String>,
    pub subtitle: Option<String>,
    pub description: Option<String>,
    pub isbn_10: Option<String>,
    pub isbn_13: Option<String>,
    pub language_code: String,
    pub publication_year: Option<i32>,
    pub publisher: Option<String>,
    pub page_count: Option<i32>,
    #[serde(default)]
    pub genres: Vec<String>,
    pub edition: Option<String>,
    pub external_ids: Option<Value>,
    #[serde(default)]
    pub authors: Vec<AuthorLink>,
    /// Optional alternative to the If-Match header for PUT.
    pub version: Option<i32>,
}

/// Body of PATCH (JSON merge-patch semantics: absent = keep, null = clear).
#[derive(Debug, Clone, Default, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct BookPatch {
    #[serde(default, deserialize_with = "double_option")]
    pub title: Option<Option<String>>,
    #[serde(default, deserialize_with = "double_option")]
    pub original_title: Option<Option<String>>,
    #[serde(default, deserialize_with = "double_option")]
    pub subtitle: Option<Option<String>>,
    #[serde(default, deserialize_with = "double_option")]
    pub description: Option<Option<String>>,
    #[serde(default, deserialize_with = "double_option")]
    pub isbn_10: Option<Option<String>>,
    #[serde(default, deserialize_with = "double_option")]
    pub isbn_13: Option<Option<String>>,
    #[serde(default, deserialize_with = "double_option")]
    pub language_code: Option<Option<String>>,
    #[serde(default, deserialize_with = "double_option")]
    pub publication_year: Option<Option<i32>>,
    #[serde(default, deserialize_with = "double_option")]
    pub publisher: Option<Option<String>>,
    #[serde(default, deserialize_with = "double_option")]
    pub page_count: Option<Option<i32>>,
    #[serde(default, deserialize_with = "double_option")]
    pub genres: Option<Option<Vec<String>>>,
    #[serde(default, deserialize_with = "double_option")]
    pub edition: Option<Option<String>>,
    #[serde(default, deserialize_with = "double_option")]
    pub external_ids: Option<Option<Value>>,
    #[serde(default, deserialize_with = "double_option")]
    pub authors: Option<Option<Vec<AuthorLink>>>,
    pub version: Option<i32>,
}

#[derive(Debug, Clone, Serialize)]
pub struct Book {
    #[serde(with = "as_string")]
    pub id: i64,
    pub title: String,
    pub original_title: Option<String>,
    pub subtitle: Option<String>,
    pub description: Option<String>,
    pub isbn_10: Option<String>,
    pub isbn_13: Option<String>,
    pub language_code: String,
    pub publication_year: Option<i16>,
    pub publisher: Option<String>,
    pub page_count: Option<i32>,
    pub genres: Vec<String>,
    pub edition: Option<String>,
    pub external_ids: Value,
    pub authors: Vec<BookAuthor>,
    pub version: i32,
    pub created_at: DateTime<Utc>,
    pub created_by: UserRef,
    pub updated_at: DateTime<Utc>,
    pub updated_by: UserRef,
    pub deleted_at: Option<DateTime<Utc>>,
}

impl BookRow {
    pub fn data(&self, links: &[AuthorLink]) -> BookData {
        BookData {
            title: self.title.clone(),
            original_title: self.original_title.clone(),
            subtitle: self.subtitle.clone(),
            description: self.description.clone(),
            isbn_10: self.isbn_10.clone(),
            isbn_13: self.isbn_13.clone(),
            language_code: self.language_code.clone(),
            publication_year: self.publication_year.map(i32::from),
            publisher: self.publisher.clone(),
            page_count: self.page_count,
            genres: self.genres.clone(),
            edition: self.edition.clone(),
            external_ids: self.external_ids.clone(),
            authors: links.to_vec(),
        }
    }

    pub fn into_book(self, authors: Vec<BookAuthor>, created_by: UserRef, updated_by: UserRef) -> Book {
        Book {
            id: self.id,
            title: self.title,
            original_title: self.original_title,
            subtitle: self.subtitle,
            description: self.description,
            isbn_10: self.isbn_10,
            isbn_13: self.isbn_13,
            language_code: self.language_code,
            publication_year: self.publication_year,
            publisher: self.publisher,
            page_count: self.page_count,
            genres: self.genres,
            edition: self.edition,
            external_ids: self.external_ids,
            authors,
            version: self.version,
            created_at: self.created_at,
            created_by,
            updated_at: self.updated_at,
            updated_by,
            deleted_at: self.deleted_at,
        }
    }
}

impl BookInput {
    pub fn into_data(self) -> BookData {
        BookData {
            title: self.title,
            original_title: self.original_title,
            subtitle: self.subtitle,
            description: self.description,
            isbn_10: self.isbn_10,
            isbn_13: self.isbn_13,
            language_code: self.language_code,
            publication_year: self.publication_year,
            publisher: self.publisher,
            page_count: self.page_count,
            genres: self.genres,
            edition: self.edition,
            external_ids: self.external_ids.unwrap_or_else(|| Value::Object(Default::default())),
            authors: self.authors,
        }
    }
}

// ---------------------------------------------------------------------------
// authors
// ---------------------------------------------------------------------------

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct AuthorRow {
    pub id: i64,
    pub full_name: String,
    pub original_name: Option<String>,
    pub birth_date: Option<NaiveDate>,
    pub death_date: Option<NaiveDate>,
    pub country_code: Option<String>,
    pub biography: Option<String>,
    pub external_ids: Value,
    pub created_at: DateTime<Utc>,
    pub created_by: i64,
    pub updated_at: DateTime<Utc>,
    pub updated_by: i64,
    pub deleted_at: Option<DateTime<Utc>>,
    pub version: i32,
}

#[derive(Debug, Clone, PartialEq, Serialize)]
pub struct AuthorData {
    pub full_name: String,
    pub original_name: Option<String>,
    pub birth_date: Option<NaiveDate>,
    pub death_date: Option<NaiveDate>,
    pub country_code: Option<String>,
    pub biography: Option<String>,
    pub external_ids: Value,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AuthorInput {
    pub full_name: String,
    pub original_name: Option<String>,
    pub birth_date: Option<NaiveDate>,
    pub death_date: Option<NaiveDate>,
    pub country_code: Option<String>,
    pub biography: Option<String>,
    pub external_ids: Option<Value>,
    pub version: Option<i32>,
}

#[derive(Debug, Clone, Default, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AuthorPatch {
    #[serde(default, deserialize_with = "double_option")]
    pub full_name: Option<Option<String>>,
    #[serde(default, deserialize_with = "double_option")]
    pub original_name: Option<Option<String>>,
    #[serde(default, deserialize_with = "double_option")]
    pub birth_date: Option<Option<NaiveDate>>,
    #[serde(default, deserialize_with = "double_option")]
    pub death_date: Option<Option<NaiveDate>>,
    #[serde(default, deserialize_with = "double_option")]
    pub country_code: Option<Option<String>>,
    #[serde(default, deserialize_with = "double_option")]
    pub biography: Option<Option<String>>,
    #[serde(default, deserialize_with = "double_option")]
    pub external_ids: Option<Option<Value>>,
    pub version: Option<i32>,
}

#[derive(Debug, Clone, Serialize)]
pub struct Author {
    #[serde(with = "as_string")]
    pub id: i64,
    pub full_name: String,
    pub original_name: Option<String>,
    pub birth_date: Option<NaiveDate>,
    pub death_date: Option<NaiveDate>,
    pub country_code: Option<String>,
    pub biography: Option<String>,
    pub external_ids: Value,
    pub version: i32,
    pub created_at: DateTime<Utc>,
    pub created_by: UserRef,
    pub updated_at: DateTime<Utc>,
    pub updated_by: UserRef,
    pub deleted_at: Option<DateTime<Utc>>,
}

impl AuthorRow {
    pub fn data(&self) -> AuthorData {
        AuthorData {
            full_name: self.full_name.clone(),
            original_name: self.original_name.clone(),
            birth_date: self.birth_date,
            death_date: self.death_date,
            country_code: self.country_code.clone(),
            biography: self.biography.clone(),
            external_ids: self.external_ids.clone(),
        }
    }

    pub fn into_author(self, created_by: UserRef, updated_by: UserRef) -> Author {
        Author {
            id: self.id,
            full_name: self.full_name,
            original_name: self.original_name,
            birth_date: self.birth_date,
            death_date: self.death_date,
            country_code: self.country_code,
            biography: self.biography,
            external_ids: self.external_ids,
            version: self.version,
            created_at: self.created_at,
            created_by,
            updated_at: self.updated_at,
            updated_by,
            deleted_at: self.deleted_at,
        }
    }
}

impl AuthorInput {
    pub fn into_data(self) -> AuthorData {
        AuthorData {
            full_name: self.full_name,
            original_name: self.original_name,
            birth_date: self.birth_date,
            death_date: self.death_date,
            country_code: self.country_code,
            biography: self.biography,
            external_ids: self.external_ids.unwrap_or_else(|| Value::Object(Default::default())),
        }
    }
}

// ---------------------------------------------------------------------------
// audit & search
// ---------------------------------------------------------------------------

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct AuditRow {
    pub id: i64,
    pub entity_type: String,
    pub entity_id: i64,
    pub action: String,
    pub changed_by: i64,
    pub changed_at: DateTime<Utc>,
    pub diff: Value,
}

#[derive(Debug, Clone, Serialize)]
pub struct AuditEntry {
    #[serde(with = "as_string")]
    pub id: i64,
    pub entity_type: String,
    #[serde(with = "as_string")]
    pub entity_id: i64,
    pub action: String,
    pub changed_by: UserRef,
    pub changed_at: DateTime<Utc>,
    pub diff: Value,
}

#[derive(Debug, Clone, Serialize, sqlx::FromRow)]
pub struct SearchHit {
    #[serde(rename = "type")]
    pub kind: String,
    #[serde(with = "as_string")]
    pub id: i64,
    pub label: String,
    pub rank: f64,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn patch_distinguishes_null_and_absent() {
        let p: BookPatch = serde_json::from_str(r#"{"subtitle": null, "title": "X"}"#).unwrap();
        assert_eq!(p.subtitle, Some(None));
        assert_eq!(p.title, Some(Some("X".into())));
        assert_eq!(p.description, None);
        assert!(serde_json::from_str::<BookPatch>(r#"{"unknown": 1}"#).is_err());
    }

    #[test]
    fn author_link_defaults() {
        let l: AuthorLink = serde_json::from_str(r#"{"author_id": "77"}"#).unwrap();
        assert_eq!(l.role, "author");
        assert_eq!(l.ordinal, None);
    }
}
