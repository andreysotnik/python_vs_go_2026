//! Parsing of list query parameters (filters / sort whitelist / pagination) and
//! construction of the corresponding keyset SQL.
//!
//! Everything here is pure (no I/O) so it is unit-tested directly; the repository
//! layer only executes what these builders produce.

use std::collections::HashMap;

use chrono::{DateTime, NaiveDate, Utc};
use serde_json::{Value, json};
use sqlx::{Postgres, QueryBuilder};

use crate::{
    config::MAX_PAGE_SIZE,
    cursor::Cursor,
    error::AppError,
    ids, isbn,
    validation::{MAX_YEAR, MIN_YEAR, is_country_code, is_language_code},
};

/// Minimum length of substring filters: shorter patterns cannot use trigram indexes.
pub const MIN_SUBSTRING_LEN: usize = 3;
pub const MAX_TEXT_PARAM_LEN: usize = 200;
/// Sentinel used for NULL publication years in the year sort index.
pub const NULL_YEAR: i64 = 32767;
pub const NULL_DATE: &str = "infinity";

pub const BOOK_TITLE_EXPR: &str = "f_unaccent(b.title || ' ' || coalesce(b.original_title, ''))";
pub const AUTHOR_NAME_EXPR: &str = "f_unaccent(a.full_name || ' ' || coalesce(a.original_name, ''))";
pub const BOOK_YEAR_EXPR: &str = "coalesce(b.publication_year, 32767::smallint)";
pub const AUTHOR_BIRTH_EXPR: &str = "coalesce(a.birth_date, 'infinity'::date)";

pub const BOOK_COLUMNS: &str = "b.id, b.title, b.original_title, b.subtitle, b.description, b.isbn_10, b.isbn_13, \
    b.language_code, b.publication_year, b.publisher, b.page_count, b.genres, b.edition, b.external_ids, \
    b.created_at, b.created_by, b.updated_at, b.updated_by, b.deleted_at, b.version";

pub const AUTHOR_COLUMNS: &str = "a.id, a.full_name, a.original_name, a.birth_date, a.death_date, a.country_code, \
    a.biography, a.external_ids, a.created_at, a.created_by, a.updated_at, a.updated_by, a.deleted_at, a.version";

// ---------------------------------------------------------------------------
// raw parameter access
// ---------------------------------------------------------------------------

/// Query-string parameters with typed, validated accessors.
pub struct Params(HashMap<String, String>);

impl Params {
    pub fn new(map: HashMap<String, String>) -> Self {
        Self(map)
    }

    /// Rejects parameters the endpoint does not know (typos would otherwise be silently ignored).
    pub fn ensure_only(&self, allowed: &[&str]) -> Result<(), AppError> {
        let mut unknown: Vec<&str> = self.0.keys().map(String::as_str).filter(|k| !allowed.contains(k)).collect();
        if unknown.is_empty() {
            return Ok(());
        }
        unknown.sort_unstable();
        Err(AppError::bad_request(format!("unknown query parameter(s): {}; allowed: {}", unknown.join(", "), allowed.join(", "))))
    }

    pub fn str(&self, key: &str) -> Result<Option<String>, AppError> {
        match self.0.get(key).map(|s| s.trim()) {
            None | Some("") => Ok(None),
            Some(s) if s.chars().count() > MAX_TEXT_PARAM_LEN => {
                Err(AppError::bad_param(key, format!("must be at most {MAX_TEXT_PARAM_LEN} characters")))
            }
            Some(s) => Ok(Some(s.to_string())),
        }
    }

    pub fn substring(&self, key: &str) -> Result<Option<String>, AppError> {
        let v = self.str(key)?;
        if let Some(s) = &v {
            if s.chars().count() < MIN_SUBSTRING_LEN {
                return Err(AppError::bad_param(key, format!("must be at least {MIN_SUBSTRING_LEN} characters")));
            }
        }
        Ok(v)
    }

    pub fn int(&self, key: &str, min: i64, max: i64) -> Result<Option<i64>, AppError> {
        match self.str(key)? {
            None => Ok(None),
            Some(s) => match s.parse::<i64>() {
                Ok(v) if (min..=max).contains(&v) => Ok(Some(v)),
                _ => Err(AppError::bad_param(key, format!("must be an integer between {min} and {max}"))),
            },
        }
    }

    pub fn id(&self, key: &str) -> Result<Option<i64>, AppError> {
        match self.str(key)? {
            None => Ok(None),
            Some(s) => ids::parse_id(&s).map(Some).ok_or_else(|| AppError::bad_param(key, "must be a positive integer id")),
        }
    }

    pub fn date(&self, key: &str) -> Result<Option<NaiveDate>, AppError> {
        match self.str(key)? {
            None => Ok(None),
            Some(s) => NaiveDate::parse_from_str(&s, "%Y-%m-%d")
                .map(Some)
                .map_err(|_| AppError::bad_param(key, "must be a date in YYYY-MM-DD format")),
        }
    }

    pub fn datetime(&self, key: &str) -> Result<Option<DateTime<Utc>>, AppError> {
        match self.str(key)? {
            None => Ok(None),
            Some(s) => {
                if let Ok(t) = DateTime::parse_from_rfc3339(&s) {
                    return Ok(Some(t.with_timezone(&Utc)));
                }
                if let Ok(d) = NaiveDate::parse_from_str(&s, "%Y-%m-%d") {
                    return Ok(d.and_hms_opt(0, 0, 0).map(|t| t.and_utc()));
                }
                Err(AppError::bad_param(key, "must be an RFC 3339 timestamp or a YYYY-MM-DD date"))
            }
        }
    }

    pub fn bool(&self, key: &str) -> Result<bool, AppError> {
        match self.str(key)?.as_deref() {
            None | Some("false") | Some("0") => Ok(false),
            Some("true") | Some("1") => Ok(true),
            Some(_) => Err(AppError::bad_param(key, "must be true or false")),
        }
    }

    pub fn limit(&self, default: u32) -> Result<u32, AppError> {
        Ok(self.int("limit", 1, i64::from(MAX_PAGE_SIZE))?.map_or(default, |v| v as u32))
    }

    /// Parses `sort=field|-field` against a whitelist; returns (field, desc).
    pub fn sort<'a>(&self, allowed: &[&'a str], default: (&'a str, bool)) -> Result<(&'a str, bool), AppError> {
        let Some(raw) = self.str("sort")? else { return Ok(default) };
        let (name, desc) = match raw.strip_prefix('-') {
            Some(n) => (n, true),
            None => (raw.strip_prefix('+').unwrap_or(&raw), false),
        };
        allowed
            .iter()
            .find(|a| **a == name)
            .map(|a| (*a, desc))
            .ok_or_else(|| AppError::bad_param("sort", format!("must be one of {} (prefix '-' for descending)", allowed.join(", "))))
    }
}

/// Escapes LIKE metacharacters so user input is matched literally.
pub fn escape_like(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 2);
    for c in s.chars() {
        if matches!(c, '%' | '_' | '\\') {
            out.push('\\');
        }
        out.push(c);
    }
    out
}

/// Common pagination state.
#[derive(Debug, Clone)]
pub struct PageReq {
    pub limit: u32,
    pub cursor: Option<Cursor>,
    pub with_total: bool,
}

// ---------------------------------------------------------------------------
// books
// ---------------------------------------------------------------------------

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum BookSort {
    Title,
    Year,
    CreatedAt,
}

impl BookSort {
    fn key(self) -> &'static str {
        match self {
            Self::Title => "books:title",
            Self::Year => "books:publication_year",
            Self::CreatedAt => "books:created_at",
        }
    }

    fn expr(self) -> &'static str {
        match self {
            Self::Title => "b.title",
            Self::Year => BOOK_YEAR_EXPR,
            Self::CreatedAt => "b.created_at",
        }
    }
}

#[derive(Debug, Clone, Default, PartialEq)]
pub struct BookFilter {
    pub title: Option<String>,
    pub author: Option<String>,
    pub author_id: Option<i64>,
    pub isbn13: Option<String>,
    pub language: Option<String>,
    pub year_from: Option<i64>,
    pub year_to: Option<i64>,
    pub publisher: Option<String>,
    pub genre: Option<String>,
    pub created_by: Option<i64>,
    pub created_from: Option<DateTime<Utc>>,
    pub created_to: Option<DateTime<Utc>>,
    pub q: Option<String>,
}

impl BookFilter {
    pub fn is_empty(&self) -> bool {
        *self == Self::default()
    }
}

#[derive(Debug, Clone)]
pub struct BookListQuery {
    pub filter: BookFilter,
    pub sort: BookSort,
    pub desc: bool,
    pub page: PageReq,
}

pub const BOOK_LIST_PARAMS: &[&str] = &[
    "title",
    "author",
    "author_id",
    "isbn",
    "language",
    "year_from",
    "year_to",
    "publisher",
    "genre",
    "created_by",
    "created_from",
    "created_to",
    "q",
    "sort",
    "limit",
    "cursor",
    "with_total",
];

impl BookListQuery {
    pub fn parse(p: &Params, default_limit: u32) -> Result<Self, AppError> {
        p.ensure_only(BOOK_LIST_PARAMS)?;
        let isbn13 = match p.str("isbn")? {
            None => None,
            Some(s) => Some(isbn::to_isbn13(&s).ok_or_else(|| AppError::bad_param("isbn", "must be a valid ISBN-10 or ISBN-13"))?),
        };
        let language = p.str("language")?.map(|s| s.to_ascii_lowercase());
        if let Some(l) = &language {
            if !is_language_code(l) {
                return Err(AppError::bad_param("language", "must be an ISO 639 code (2-3 letters)"));
            }
        }
        let filter = BookFilter {
            title: p.substring("title")?,
            author: p.substring("author")?,
            author_id: p.id("author_id")?,
            isbn13,
            language,
            year_from: p.int("year_from", i64::from(MIN_YEAR), i64::from(MAX_YEAR))?,
            year_to: p.int("year_to", i64::from(MIN_YEAR), i64::from(MAX_YEAR))?,
            publisher: p.str("publisher")?,
            genre: p.str("genre")?.map(|g| g.to_lowercase()),
            created_by: p.id("created_by")?,
            created_from: p.datetime("created_from")?,
            created_to: p.datetime("created_to")?,
            q: p.str("q")?,
        };
        if let (Some(a), Some(b)) = (filter.year_from, filter.year_to) {
            if a > b {
                return Err(AppError::bad_param("year_from", "must not be greater than year_to"));
            }
        }
        let (field, desc) = p.sort(&["title", "publication_year", "created_at"], ("created_at", true))?;
        let sort = match field {
            "title" => BookSort::Title,
            "publication_year" => BookSort::Year,
            _ => BookSort::CreatedAt,
        };
        let cursor = p.str("cursor")?.map(|c| Cursor::decode_for(&c, sort.key(), desc)).transpose()?;
        Ok(Self { filter, sort, desc, page: PageReq { limit: p.limit(default_limit)?, cursor, with_total: p.bool("with_total")? } })
    }

    /// Cursor pointing after the given row.
    pub fn cursor_after(&self, row: &crate::models::BookRow) -> String {
        let v = match self.sort {
            BookSort::Title => json!(row.title),
            BookSort::Year => json!(row.publication_year.map_or(NULL_YEAR, i64::from)),
            BookSort::CreatedAt => json!(ts_to_cursor(&row.created_at)),
        };
        Cursor::new(self.sort.key(), self.desc, v, row.id).encode()
    }
}

pub fn ts_to_cursor(t: &DateTime<Utc>) -> String {
    t.to_rfc3339_opts(chrono::SecondsFormat::Micros, true)
}

fn cursor_ts(c: &Cursor) -> Result<DateTime<Utc>, AppError> {
    DateTime::parse_from_rfc3339(c.value_str()?)
        .map(|t| t.with_timezone(&Utc))
        .map_err(|_| AppError::bad_param("cursor", "malformed cursor (timestamp)"))
}

/// Appends the FROM clause. With an author-name filter the matching book ids are produced
/// first (trigram search on authors -> author_books) and each book is fetched by primary key
/// through LATERAL lookups. This keeps the plan a chain of index nested loops even as a
/// *generic* plan, where the planner cannot know how selective the name pattern is and
/// would otherwise hash-join against a full scan of books. `LIMIT 1` (id is the primary key)
/// is an optimization fence: it stops the planner from flattening the LATERAL subquery back
/// into a plain join.
pub fn push_book_from(qb: &mut QueryBuilder<'static, Postgres>, f: &BookFilter) {
    match &f.author {
        None => {
            qb.push(" FROM books b");
        }
        Some(v) => {
            qb.push(format_args!(
                " FROM (SELECT DISTINCT ab.book_id FROM authors a \
                 CROSS JOIN LATERAL (SELECT ab0.book_id FROM author_books ab0 WHERE ab0.author_id = a.id) ab \
                 WHERE a.deleted_at IS NULL AND {AUTHOR_NAME_EXPR} ILIKE '%' || f_unaccent("
            ));
            qb.push_bind(escape_like(v));
            qb.push(") || '%') ids CROSS JOIN LATERAL (SELECT * FROM books b0 WHERE b0.id = ids.book_id LIMIT 1) b");
        }
    }
}

/// Appends the WHERE clause for the book filters (shared by page and estimate queries).
pub fn push_book_filters(qb: &mut QueryBuilder<'static, Postgres>, f: &BookFilter) {
    qb.push(" WHERE b.deleted_at IS NULL");
    if let Some(v) = &f.title {
        qb.push(format_args!(" AND {BOOK_TITLE_EXPR} ILIKE '%' || f_unaccent("));
        qb.push_bind(escape_like(v));
        qb.push(") || '%'");
    }
    if let Some(v) = f.author_id {
        qb.push(" AND b.id IN (SELECT ab.book_id FROM author_books ab WHERE ab.author_id = ");
        qb.push_bind(v);
        qb.push(")");
    }
    if let Some(v) = &f.isbn13 {
        qb.push(" AND b.id = (SELECT r.book_id FROM isbn_registry r WHERE r.isbn_13 = ");
        qb.push_bind(v.clone());
        qb.push(")");
    }
    if let Some(v) = &f.language {
        qb.push(" AND b.language_code = ");
        qb.push_bind(v.clone());
    }
    if f.year_from.is_some() || f.year_to.is_some() {
        qb.push(" AND b.publication_year IS NOT NULL");
    }
    if let Some(v) = f.year_from {
        qb.push(format_args!(" AND {BOOK_YEAR_EXPR} >= "));
        qb.push_bind(v as i16);
    }
    if let Some(v) = f.year_to {
        qb.push(format_args!(" AND {BOOK_YEAR_EXPR} <= "));
        qb.push_bind(v as i16);
    }
    if let Some(v) = &f.publisher {
        qb.push(" AND b.publisher = ");
        qb.push_bind(v.clone());
    }
    if let Some(v) = &f.genre {
        qb.push(" AND b.genres @> ARRAY[");
        qb.push_bind(v.clone());
        qb.push("::text]");
    }
    if let Some(v) = f.created_by {
        qb.push(" AND b.created_by = ");
        qb.push_bind(v);
    }
    if let Some(v) = f.created_from {
        qb.push(" AND b.created_at >= ");
        qb.push_bind(v);
    }
    if let Some(v) = f.created_to {
        qb.push(" AND b.created_at < ");
        qb.push_bind(v);
    }
    if let Some(v) = &f.q {
        qb.push(" AND b.search_vector @@ websearch_to_tsquery('simple', f_unaccent(");
        qb.push_bind(v.clone());
        qb.push("))");
    }
}

pub fn build_book_list(q: &BookListQuery) -> Result<QueryBuilder<'static, Postgres>, AppError> {
    build_book_list_prefixed("", q)
}

/// Same as [`build_book_list`] with a prefix such as `EXPLAIN (ANALYZE) ` (used by the plan checker).
pub fn build_book_list_prefixed(prefix: &str, q: &BookListQuery) -> Result<QueryBuilder<'static, Postgres>, AppError> {
    let mut qb = QueryBuilder::new(format!("{prefix}SELECT {BOOK_COLUMNS}"));
    push_book_from(&mut qb, &q.filter);
    push_book_filters(&mut qb, &q.filter);
    let expr = q.sort.expr();
    let (op, dir) = if q.desc { ("<", "DESC") } else { (">", "ASC") };
    if let Some(c) = &q.page.cursor {
        let id = c.id_value().unwrap_or_default();
        qb.push(format_args!(" AND ({expr}, b.id) {op} ("));
        match q.sort {
            BookSort::Title => {
                qb.push_bind(c.value_str()?.to_string());
            }
            BookSort::Year => {
                let y = c.value_i64()?;
                let y = i16::try_from(y).map_err(|_| AppError::bad_param("cursor", "malformed cursor (year)"))?;
                qb.push_bind(y);
            }
            BookSort::CreatedAt => {
                qb.push_bind(cursor_ts(c)?);
            }
        }
        qb.push(", ");
        qb.push_bind(id);
        qb.push(")");
    }
    qb.push(format_args!(" ORDER BY {expr} {dir}, b.id {dir} LIMIT "));
    qb.push_bind(i64::from(q.page.limit) + 1);
    Ok(qb)
}

pub fn build_book_estimate(f: &BookFilter) -> QueryBuilder<'static, Postgres> {
    let mut qb = QueryBuilder::new("EXPLAIN (FORMAT JSON) SELECT 1");
    push_book_from(&mut qb, f);
    push_book_filters(&mut qb, f);
    qb
}

// ---------------------------------------------------------------------------
// authors
// ---------------------------------------------------------------------------

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AuthorSort {
    FullName,
    BirthDate,
    CreatedAt,
}

impl AuthorSort {
    fn key(self) -> &'static str {
        match self {
            Self::FullName => "authors:full_name",
            Self::BirthDate => "authors:birth_date",
            Self::CreatedAt => "authors:created_at",
        }
    }

    fn expr(self) -> &'static str {
        match self {
            Self::FullName => "a.full_name",
            Self::BirthDate => AUTHOR_BIRTH_EXPR,
            Self::CreatedAt => "a.created_at",
        }
    }
}

#[derive(Debug, Clone, Default, PartialEq)]
pub struct AuthorFilter {
    pub name: Option<String>,
    pub country: Option<String>,
    pub born_from: Option<NaiveDate>,
    pub born_to: Option<NaiveDate>,
    pub q: Option<String>,
}

impl AuthorFilter {
    pub fn is_empty(&self) -> bool {
        *self == Self::default()
    }
}

#[derive(Debug, Clone)]
pub struct AuthorListQuery {
    pub filter: AuthorFilter,
    pub sort: AuthorSort,
    pub desc: bool,
    pub page: PageReq,
}

pub const AUTHOR_LIST_PARAMS: &[&str] = &["name", "country", "born_from", "born_to", "q", "sort", "limit", "cursor", "with_total"];

impl AuthorListQuery {
    pub fn parse(p: &Params, default_limit: u32) -> Result<Self, AppError> {
        p.ensure_only(AUTHOR_LIST_PARAMS)?;
        let country = p.str("country")?.map(|s| s.to_ascii_uppercase());
        if let Some(c) = &country {
            if !is_country_code(c) {
                return Err(AppError::bad_param("country", "must be an ISO 3166-1 alpha-2 code"));
            }
        }
        let filter = AuthorFilter {
            name: p.substring("name")?,
            country,
            born_from: p.date("born_from")?,
            born_to: p.date("born_to")?,
            q: p.str("q")?,
        };
        if let (Some(a), Some(b)) = (filter.born_from, filter.born_to) {
            if a > b {
                return Err(AppError::bad_param("born_from", "must not be later than born_to"));
            }
        }
        let (field, desc) = p.sort(&["full_name", "birth_date", "created_at"], ("created_at", true))?;
        let sort = match field {
            "full_name" => AuthorSort::FullName,
            "birth_date" => AuthorSort::BirthDate,
            _ => AuthorSort::CreatedAt,
        };
        let cursor = p.str("cursor")?.map(|c| Cursor::decode_for(&c, sort.key(), desc)).transpose()?;
        Ok(Self { filter, sort, desc, page: PageReq { limit: p.limit(default_limit)?, cursor, with_total: p.bool("with_total")? } })
    }

    pub fn cursor_after(&self, row: &crate::models::AuthorRow) -> String {
        let v = match self.sort {
            AuthorSort::FullName => json!(row.full_name),
            AuthorSort::BirthDate => json!(row.birth_date.map_or_else(|| NULL_DATE.to_string(), |d| d.format("%Y-%m-%d").to_string())),
            AuthorSort::CreatedAt => json!(ts_to_cursor(&row.created_at)),
        };
        Cursor::new(self.sort.key(), self.desc, v, row.id).encode()
    }
}

pub fn push_author_filters(qb: &mut QueryBuilder<'static, Postgres>, f: &AuthorFilter) {
    qb.push(" WHERE a.deleted_at IS NULL");
    if let Some(v) = &f.name {
        qb.push(format_args!(" AND {AUTHOR_NAME_EXPR} ILIKE '%' || f_unaccent("));
        qb.push_bind(escape_like(v));
        qb.push(") || '%'");
    }
    if let Some(v) = &f.country {
        qb.push(" AND a.country_code = ");
        qb.push_bind(v.clone());
    }
    if f.born_from.is_some() || f.born_to.is_some() {
        qb.push(" AND a.birth_date IS NOT NULL");
    }
    if let Some(v) = f.born_from {
        qb.push(format_args!(" AND {AUTHOR_BIRTH_EXPR} >= "));
        qb.push_bind(v);
    }
    if let Some(v) = f.born_to {
        qb.push(format_args!(" AND {AUTHOR_BIRTH_EXPR} <= "));
        qb.push_bind(v);
    }
    if let Some(v) = &f.q {
        qb.push(" AND a.search_vector @@ websearch_to_tsquery('simple', f_unaccent(");
        qb.push_bind(v.clone());
        qb.push("))");
    }
}

pub fn build_author_list(q: &AuthorListQuery) -> Result<QueryBuilder<'static, Postgres>, AppError> {
    build_author_list_prefixed("", q)
}

pub fn build_author_list_prefixed(prefix: &str, q: &AuthorListQuery) -> Result<QueryBuilder<'static, Postgres>, AppError> {
    let mut qb = QueryBuilder::new(format!("{prefix}SELECT {AUTHOR_COLUMNS} FROM authors a"));
    push_author_filters(&mut qb, &q.filter);
    let expr = q.sort.expr();
    let (op, dir) = if q.desc { ("<", "DESC") } else { (">", "ASC") };
    if let Some(c) = &q.page.cursor {
        let id = c.id_value().unwrap_or_default();
        qb.push(format_args!(" AND ({expr}, a.id) {op} ("));
        match q.sort {
            AuthorSort::FullName => {
                qb.push_bind(c.value_str()?.to_string());
            }
            AuthorSort::BirthDate => {
                let s = c.value_str()?;
                if s != NULL_DATE && NaiveDate::parse_from_str(s, "%Y-%m-%d").is_err() {
                    return Err(AppError::bad_param("cursor", "malformed cursor (date)"));
                }
                qb.push_bind(s.to_string());
                qb.push("::date");
            }
            AuthorSort::CreatedAt => {
                qb.push_bind(cursor_ts(c)?);
            }
        }
        qb.push(", ");
        qb.push_bind(id);
        qb.push(")");
    }
    qb.push(format_args!(" ORDER BY {expr} {dir}, a.id {dir} LIMIT "));
    qb.push_bind(i64::from(q.page.limit) + 1);
    Ok(qb)
}

pub fn build_author_estimate(f: &AuthorFilter) -> QueryBuilder<'static, Postgres> {
    let mut qb = QueryBuilder::new("EXPLAIN (FORMAT JSON) SELECT 1 FROM authors a");
    push_author_filters(&mut qb, f);
    qb
}

// ---------------------------------------------------------------------------
// simple id-ordered pages (books of an author, history)
// ---------------------------------------------------------------------------

/// Pagination for endpoints with a fixed order: `(limit, cursor, with_total)` only.
pub fn parse_simple_page(p: &Params, key: &str, desc: bool, default_limit: u32) -> Result<PageReq, AppError> {
    p.ensure_only(&["limit", "cursor"])?;
    let cursor = p.str("cursor")?.map(|c| Cursor::decode_for(&c, key, desc)).transpose()?;
    Ok(PageReq { limit: p.limit(default_limit)?, cursor, with_total: false })
}

/// Pagination for the audit history: keyset over (changed_at, id) ascending.
pub fn history_cursor_value(c: &Cursor) -> Result<(DateTime<Utc>, i64), AppError> {
    Ok((cursor_ts(c)?, c.id_value().unwrap_or_default()))
}

// ---------------------------------------------------------------------------
// search
// ---------------------------------------------------------------------------

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SearchType {
    Books,
    Authors,
    All,
}

/// How matches are found. Chosen on the first page (full text; fuzzy only if full text finds
/// nothing) and carried in the cursor so all pages of one result set use the same rule.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SearchMode {
    /// Full-text match; rank = ts_rank + trigram similarity of the title/name.
    FullText,
    /// No full-text hit at all (typo, partial word): trigram word-similarity match and rank.
    Fuzzy,
}

#[derive(Debug, Clone)]
pub struct SearchQuery {
    pub q: String,
    pub kind: SearchType,
    pub page: PageReq,
}

pub const SEARCH_PARAMS: &[&str] = &["q", "type", "limit", "cursor", "with_total"];

/// Parsed search cursor: (rank, kind_ord, id, mode).
pub type SearchAfter = (f64, i32, i64, SearchMode);

impl SearchQuery {
    pub fn parse(p: &Params, default_limit: u32) -> Result<Self, AppError> {
        p.ensure_only(SEARCH_PARAMS)?;
        let q = p.str("q")?.ok_or_else(|| AppError::bad_param("q", "is required"))?;
        if q.chars().count() < 2 {
            return Err(AppError::bad_param("q", "must be at least 2 characters"));
        }
        let kind = match p.str("type")?.as_deref() {
            None | Some("all") => SearchType::All,
            Some("books") => SearchType::Books,
            Some("authors") => SearchType::Authors,
            Some(_) => return Err(AppError::bad_param("type", "must be one of books, authors, all")),
        };
        let key = Self::key(kind);
        let cursor = p.str("cursor")?.map(|c| Cursor::decode_for(&c, key, true)).transpose()?;
        if let Some(c) = &cursor {
            Self::cursor_parts(c)?;
        }
        Ok(Self { q, kind, page: PageReq { limit: p.limit(default_limit)?, cursor, with_total: p.bool("with_total")? } })
    }

    fn key(kind: SearchType) -> &'static str {
        match kind {
            SearchType::Books => "search:books",
            SearchType::Authors => "search:authors",
            SearchType::All => "search:all",
        }
    }

    /// Cursor value is `[rank, kind_ord, mode]`; kind_ord breaks ties between tables.
    pub fn cursor_parts(c: &Cursor) -> Result<SearchAfter, AppError> {
        let bad = || AppError::bad_param("cursor", "malformed cursor (rank)");
        let arr = c.value.as_array().ok_or_else(bad)?;
        let rank = arr.first().and_then(Value::as_f64).ok_or_else(bad)?;
        let kind = arr.get(1).and_then(Value::as_i64).and_then(|k| i32::try_from(k).ok()).ok_or_else(bad)?;
        let mode = match arr.get(2).and_then(Value::as_i64) {
            Some(0) => SearchMode::FullText,
            Some(1) => SearchMode::Fuzzy,
            _ => return Err(bad()),
        };
        Ok((rank, kind, c.id_value().unwrap_or_default(), mode))
    }

    pub fn cursor_after(&self, hit: &crate::models::SearchHit, mode: SearchMode) -> String {
        let kind_ord = i32::from(hit.kind == "book");
        let m = i32::from(mode == SearchMode::Fuzzy);
        Cursor::new(Self::key(self.kind), true, json!([hit.rank, kind_ord, m]), hit.id).encode()
    }
}

const TSQ: &str = "websearch_to_tsquery('simple', f_unaccent($1))";

/// Full-text mode re-ranks only the best `SEARCH_CANDIDATES` matches (by ts_rank, id as
/// tie-break, i.e. a deterministic set) with the trigram similarity term: computing
/// unaccent+similarity for every match of a frequent word cost ~80% of the query (25k
/// matches: 100 ms -> ~20 ms). Consequence: a search returns at most this many hits per type.
pub const SEARCH_CANDIDATES: i64 = 1000;

fn book_branch(mode: SearchMode) -> String {
    match mode {
        SearchMode::FullText => format!(
            "SELECT 'book'::text AS kind, 1 AS kind_ord, b.id, b.title AS label, \
             (b.tr + similarity(f_unaccent($1), {BOOK_TITLE_EXPR}))::float8 AS rank \
             FROM (SELECT b.id, b.title, b.original_title, ts_rank(b.search_vector, {TSQ}) AS tr \
                   FROM books b WHERE b.deleted_at IS NULL AND b.search_vector @@ {TSQ} \
                   ORDER BY tr DESC, b.id DESC LIMIT {SEARCH_CANDIDATES}) b"
        ),
        SearchMode::Fuzzy => format!(
            "SELECT 'book'::text AS kind, 1 AS kind_ord, b.id, b.title AS label, \
             word_similarity(f_unaccent($1), {BOOK_TITLE_EXPR})::float8 AS rank \
             FROM books b WHERE b.deleted_at IS NULL AND f_unaccent($1) <% {BOOK_TITLE_EXPR}"
        ),
    }
}

fn author_branch(mode: SearchMode) -> String {
    match mode {
        SearchMode::FullText => format!(
            "SELECT 'author'::text AS kind, 0 AS kind_ord, a.id, a.full_name AS label, \
             (a.tr + similarity(f_unaccent($1), {AUTHOR_NAME_EXPR}))::float8 AS rank \
             FROM (SELECT a.id, a.full_name, a.original_name, ts_rank(a.search_vector, {TSQ}) AS tr \
                   FROM authors a WHERE a.deleted_at IS NULL AND a.search_vector @@ {TSQ} \
                   ORDER BY tr DESC, a.id DESC LIMIT {SEARCH_CANDIDATES}) a"
        ),
        SearchMode::Fuzzy => format!(
            "SELECT 'author'::text AS kind, 0 AS kind_ord, a.id, a.full_name AS label, \
             word_similarity(f_unaccent($1), {AUTHOR_NAME_EXPR})::float8 AS rank \
             FROM authors a WHERE a.deleted_at IS NULL AND f_unaccent($1) <% {AUTHOR_NAME_EXPR}"
        ),
    }
}

fn search_inner(kind: SearchType, mode: SearchMode) -> String {
    match kind {
        SearchType::Books => book_branch(mode),
        SearchType::Authors => author_branch(mode),
        SearchType::All => format!("{} UNION ALL {}", book_branch(mode), author_branch(mode)),
    }
}

/// Search SQL. Binds: $1 = q, $2 = limit+1, $3 = cursor rank, $4 = cursor kind, $5 = cursor id
/// (the three cursor binds are NULL on the first page).
pub fn search_sql(kind: SearchType, mode: SearchMode) -> String {
    format!(
        "SELECT kind, id, label, rank FROM ({}) s \
         WHERE ($3::float8 IS NULL OR (s.rank, s.kind_ord, s.id) < ($3::float8, $4::int, $5::bigint)) \
         ORDER BY s.rank DESC, s.kind_ord DESC, s.id DESC LIMIT $2",
        search_inner(kind, mode)
    )
}

/// EXPLAIN-based estimate of the number of search matches. Bind: $1 = q.
pub fn search_estimate_sql(kind: SearchType, mode: SearchMode) -> String {
    format!("EXPLAIN (FORMAT JSON) SELECT 1 FROM ({}) s", search_inner(kind, mode))
}

/// Extracts the top-level "Plan Rows" from `EXPLAIN (FORMAT JSON)` output.
pub fn plan_rows(explain: &Value) -> Option<i64> {
    explain.get(0)?.get("Plan")?.get("Plan Rows")?.as_f64().map(|r| r.round() as i64)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::models::BookRow;

    fn params(pairs: &[(&str, &str)]) -> Params {
        Params::new(pairs.iter().map(|(k, v)| ((*k).to_string(), (*v).to_string())).collect())
    }

    #[test]
    fn defaults() {
        let q = BookListQuery::parse(&params(&[]), 20).unwrap();
        assert_eq!(q.sort, BookSort::CreatedAt);
        assert!(q.desc);
        assert_eq!(q.page.limit, 20);
        assert!(q.filter.is_empty());
        assert!(!q.page.with_total);
        let sql = build_book_list(&q).unwrap().into_sql();
        assert!(sql.contains("ORDER BY b.created_at DESC, b.id DESC LIMIT $1"), "{sql}");
        assert!(!sql.to_uppercase().contains("OFFSET"));
    }

    #[test]
    fn all_book_filters_are_rendered() {
        let isbn = "978-0-201-61622-4";
        let q = BookListQuery::parse(
            &params(&[
                ("title", "war"),
                ("author", "tolstoy"),
                ("author_id", "12"),
                ("isbn", isbn),
                ("language", "RU"),
                ("year_from", "1800"),
                ("year_to", "1900"),
                ("publisher", "Penguin"),
                ("genre", "Novel"),
                ("created_by", "1"),
                ("created_from", "2024-01-01"),
                ("created_to", "2025-01-01T00:00:00Z"),
                ("q", "war peace"),
                ("sort", "-title"),
                ("limit", "50"),
                ("with_total", "true"),
            ]),
            20,
        )
        .unwrap();
        assert_eq!(q.filter.language.as_deref(), Some("ru"));
        assert_eq!(q.filter.genre.as_deref(), Some("novel"));
        assert_eq!(q.filter.isbn13.as_deref(), Some("9780201616224"));
        assert!(q.page.with_total);
        let sql = build_book_list(&q).unwrap().into_sql();
        for frag in [
            "ILIKE '%' || f_unaccent($1) || '%') ids CROSS JOIN LATERAL (SELECT * FROM books b0 WHERE b0.id = ids.book_id LIMIT 1) b",
            "coalesce(b.original_title, '')) ILIKE '%' || f_unaccent($2) || '%'",
            "ab.author_id = $3",
            "isbn_registry r WHERE r.isbn_13 = $4",
            "b.language_code = $5",
            "b.publication_year IS NOT NULL",
            "coalesce(b.publication_year, 32767::smallint) >= $6",
            "coalesce(b.publication_year, 32767::smallint) <= $7",
            "b.publisher = $8",
            "b.genres @> ARRAY[$9::text]",
            "b.created_by = $10",
            "b.created_at >= $11",
            "b.created_at < $12",
            "websearch_to_tsquery('simple', f_unaccent($13))",
            "ORDER BY b.title DESC, b.id DESC LIMIT $14",
        ] {
            assert!(sql.contains(frag), "missing `{frag}` in {sql}");
        }
        let est = build_book_estimate(&q.filter).into_sql();
        assert!(est.starts_with("EXPLAIN (FORMAT JSON) SELECT 1 FROM (SELECT DISTINCT ab.book_id FROM authors a"));
        let est = build_book_estimate(&BookFilter::default()).into_sql();
        assert_eq!(est, "EXPLAIN (FORMAT JSON) SELECT 1 FROM books b WHERE b.deleted_at IS NULL");
        assert!(!est.contains("LIMIT"));
    }

    fn row() -> BookRow {
        BookRow {
            id: 77,
            title: "T".into(),
            original_title: None,
            subtitle: None,
            description: None,
            isbn_10: None,
            isbn_13: None,
            language_code: "en".into(),
            publication_year: None,
            publisher: None,
            page_count: None,
            genres: vec![],
            edition: None,
            external_ids: json!({}),
            created_at: Utc::now(),
            created_by: 1,
            updated_at: Utc::now(),
            updated_by: 1,
            deleted_at: None,
            version: 1,
        }
    }

    #[test]
    fn keyset_predicates_per_sort() {
        for (sort, frag, op) in [
            ("title", "(b.title, b.id) > ($1, $2)", "ASC"),
            ("-publication_year", "(coalesce(b.publication_year, 32767::smallint), b.id) < ($1, $2)", "DESC"),
            ("created_at", "(b.created_at, b.id) > ($1, $2)", "ASC"),
        ] {
            let first = BookListQuery::parse(&params(&[("sort", sort)]), 20).unwrap();
            let cursor = first.cursor_after(&row());
            let q = BookListQuery::parse(&params(&[("sort", sort), ("cursor", &cursor)]), 20).unwrap();
            let sql = build_book_list(&q).unwrap().into_sql();
            assert!(sql.contains(frag), "{sql}");
            assert!(sql.contains(&format!("b.id {op} LIMIT $3")), "{sql}");
        }
    }

    #[test]
    fn null_year_cursor_uses_sentinel() {
        let q = BookListQuery::parse(&params(&[("sort", "publication_year")]), 20).unwrap();
        let c = Cursor::decode(&q.cursor_after(&row())).unwrap();
        assert_eq!(c.value_i64().unwrap(), NULL_YEAR);
    }

    #[test]
    fn invalid_book_params() {
        for bad in [
            vec![("limit", "0")],
            vec![("limit", "101")],
            vec![("limit", "x")],
            vec![("sort", "isbn_13")],
            vec![("title", "ab")],
            vec![("author", "x")],
            vec![("author_id", "abc")],
            vec![("isbn", "123")],
            vec![("language", "english")],
            vec![("year_from", "1900"), ("year_to", "1800")],
            vec![("year_from", "99999")],
            vec![("created_from", "yesterday")],
            vec![("with_total", "maybe")],
            vec![("cursor", "garbage")],
            vec![("offset", "10")],
        ] {
            let err = BookListQuery::parse(&params(&bad), 20).unwrap_err();
            assert_eq!(err.status(), axum::http::StatusCode::BAD_REQUEST, "{bad:?}");
        }
        let long = "x".repeat(MAX_TEXT_PARAM_LEN + 1);
        assert!(BookListQuery::parse(&params(&[("q", &long)]), 20).is_err());
    }

    #[test]
    fn cursor_bound_to_sort() {
        let q = BookListQuery::parse(&params(&[("sort", "title")]), 20).unwrap();
        let c = q.cursor_after(&row());
        assert!(BookListQuery::parse(&params(&[("sort", "-title"), ("cursor", &c)]), 20).is_err());
        assert!(AuthorListQuery::parse(&params(&[("sort", "full_name"), ("cursor", &c)]), 20).is_err());
        // a cursor whose value has the wrong type is rejected when building SQL
        let bad = Cursor::new("books:publication_year", false, json!("x"), 1).encode();
        let q = BookListQuery::parse(&params(&[("sort", "publication_year"), ("cursor", &bad)]), 20).unwrap();
        assert!(build_book_list(&q).is_err());
        let bad = Cursor::new("books:created_at", true, json!("not-a-ts"), 1).encode();
        let q = BookListQuery::parse(&params(&[("cursor", &bad)]), 20).unwrap();
        assert!(build_book_list(&q).is_err());
    }

    #[test]
    fn author_queries() {
        let q = AuthorListQuery::parse(
            &params(&[
                ("name", "tolst"),
                ("country", "ru"),
                ("born_from", "1800-01-01"),
                ("born_to", "1900-12-31"),
                ("q", "leo"),
                ("sort", "birth_date"),
            ]),
            20,
        )
        .unwrap();
        assert_eq!(q.filter.country.as_deref(), Some("RU"));
        let sql = build_author_list(&q).unwrap().into_sql();
        for frag in [
            "f_unaccent(a.full_name || ' ' || coalesce(a.original_name, '')) ILIKE",
            "a.country_code = $2",
            "coalesce(a.birth_date, 'infinity'::date) >= $3",
            "coalesce(a.birth_date, 'infinity'::date) <= $4",
            "a.search_vector @@",
            "ORDER BY coalesce(a.birth_date, 'infinity'::date) ASC, a.id ASC",
        ] {
            assert!(sql.contains(frag), "missing `{frag}` in {sql}");
        }
        assert!(build_author_estimate(&q.filter).into_sql().starts_with("EXPLAIN"));

        for sort in ["full_name", "-birth_date", "created_at"] {
            let q = AuthorListQuery::parse(&params(&[("sort", sort)]), 20).unwrap();
            let r = crate::models::AuthorRow {
                id: 5,
                full_name: "A".into(),
                original_name: None,
                birth_date: None,
                death_date: None,
                country_code: None,
                biography: None,
                external_ids: json!({}),
                created_at: Utc::now(),
                created_by: 1,
                updated_at: Utc::now(),
                updated_by: 1,
                deleted_at: None,
                version: 1,
            };
            let c = q.cursor_after(&r);
            let q2 = AuthorListQuery::parse(&params(&[("sort", sort), ("cursor", &c)]), 20).unwrap();
            let sql = build_author_list(&q2).unwrap().into_sql();
            assert!(sql.contains(", a.id) "), "{sql}");
        }
        let bad = Cursor::new("authors:birth_date", false, json!("31-12-1900"), 1).encode();
        let q = AuthorListQuery::parse(&params(&[("sort", "birth_date"), ("cursor", &bad)]), 20).unwrap();
        assert!(build_author_list(&q).is_err());
        for bad in [
            vec![("country", "RUS")],
            vec![("born_from", "1900")],
            vec![("born_from", "1950-01-01"), ("born_to", "1900-01-01")],
            vec![("name", "ab")],
        ] {
            assert!(AuthorListQuery::parse(&params(&bad), 20).is_err(), "{bad:?}");
        }
    }

    #[test]
    fn search_parsing() {
        assert!(SearchQuery::parse(&params(&[]), 20).is_err());
        assert!(SearchQuery::parse(&params(&[("q", "a")]), 20).is_err());
        assert!(SearchQuery::parse(&params(&[("q", "abc"), ("type", "movies")]), 20).is_err());
        let q = SearchQuery::parse(&params(&[("q", "tolstoy"), ("type", "authors")]), 20).unwrap();
        assert_eq!(q.kind, SearchType::Authors);
        let hit = crate::models::SearchHit { kind: "author".into(), id: 9, label: "x".into(), rank: 0.25 };
        let c = q.cursor_after(&hit, SearchMode::Fuzzy);
        let q2 = SearchQuery::parse(&params(&[("q", "tolstoy"), ("type", "authors"), ("cursor", &c)]), 20).unwrap();
        assert_eq!(SearchQuery::cursor_parts(q2.page.cursor.as_ref().unwrap()).unwrap(), (0.25, 0, 9, SearchMode::Fuzzy));
        let c = q.cursor_after(&hit, SearchMode::FullText);
        assert_eq!(SearchQuery::cursor_parts(&Cursor::decode(&c).unwrap()).unwrap().3, SearchMode::FullText);
        let bad_mode = Cursor::new("search:authors", true, json!([0.1, 0, 7]), 1).encode();
        assert!(SearchQuery::parse(&params(&[("q", "tolstoy"), ("type", "authors"), ("cursor", &bad_mode)]), 20).is_err());
        assert!(SearchQuery::parse(&params(&[("q", "tolstoy"), ("cursor", &c)]), 20).is_err());
        let bad = Cursor::new("search:all", true, json!("x"), 1).encode();
        assert!(SearchQuery::parse(&params(&[("q", "tolstoy"), ("cursor", &bad)]), 20).is_err());
        let sql = search_sql(SearchType::All, SearchMode::FullText);
        assert!(sql.contains("UNION ALL") && sql.contains("ORDER BY s.rank DESC, s.kind_ord DESC, s.id DESC LIMIT $2"));
        assert!(sql.contains("ts_rank(") && sql.contains("similarity(") && !sql.contains("<%"));
        let fuzzy = search_sql(SearchType::Books, SearchMode::Fuzzy);
        assert!(fuzzy.contains("<%") && fuzzy.contains("word_similarity(") && !fuzzy.contains("authors a"));
        assert!(search_estimate_sql(SearchType::Authors, SearchMode::Fuzzy).starts_with("EXPLAIN"));
    }

    #[test]
    fn misc_helpers() {
        assert_eq!(escape_like("50%_a\\b"), "50\\%\\_a\\\\b");
        assert_eq!(plan_rows(&json!([{"Plan": {"Plan Rows": 1234.4}}])), Some(1234));
        assert_eq!(plan_rows(&json!({})), None);
        let p = params(&[("limit", "5"), ("cursor", "zzz")]);
        assert!(parse_simple_page(&p, "k", false, 20).is_err());
        let c = Cursor::new("k", false, json!(ts_to_cursor(&Utc::now())), 3).encode();
        let page = parse_simple_page(&params(&[("cursor", &c)]), "k", false, 20).unwrap();
        assert_eq!(history_cursor_value(page.cursor.as_ref().unwrap()).unwrap().1, 3);
        assert!(parse_simple_page(&params(&[("sort", "x")]), "k", false, 20).is_err());
        assert_eq!(params(&[("sort", "+title")]).sort(&["title"], ("title", true)).unwrap(), ("title", false));
    }
}
