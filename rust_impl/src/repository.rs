//! Repository layer: SQL only, no business rules. Every function takes an explicit
//! connection/transaction so the service layer controls transaction boundaries.

use std::collections::HashMap;

use chrono::{DateTime, Utc};
use serde_json::Value;
use sqlx::{PgConnection, Postgres, QueryBuilder};

use crate::{
    error::AppResult,
    models::{AuditRow, AuthorData, AuthorLink, AuthorRow, BookAuthor, BookData, BookRow, SearchHit, User, UserInput},
    query::{self, AUTHOR_COLUMNS, BOOK_COLUMNS},
};

fn returning(cols: &str, alias: &str) -> String {
    cols.replace(alias, "")
}

// ---------------------------------------------------------------------------
// generic helpers
// ---------------------------------------------------------------------------

pub async fn fetch_books(conn: &mut PgConnection, mut qb: QueryBuilder<'static, Postgres>) -> AppResult<Vec<BookRow>> {
    Ok(qb.build_query_as::<BookRow>().fetch_all(conn).await?)
}

pub async fn fetch_authors(conn: &mut PgConnection, mut qb: QueryBuilder<'static, Postgres>) -> AppResult<Vec<AuthorRow>> {
    Ok(qb.build_query_as::<AuthorRow>().fetch_all(conn).await?)
}

/// Runs an `EXPLAIN (FORMAT JSON)` query and returns the planner's row estimate.
pub async fn explain_estimate(conn: &mut PgConnection, mut qb: QueryBuilder<'static, Postgres>) -> AppResult<Option<i64>> {
    let (plan,): (Value,) = qb.build_query_as().fetch_one(conn).await?;
    Ok(query::plan_rows(&plan))
}

/// Table size estimate from pg_class.reltuples summed over partitions (no COUNT(*)).
pub async fn reltuples(conn: &mut PgConnection, table: &str) -> AppResult<i64> {
    let (n,): (i64,) = sqlx::query_as(
        "SELECT coalesce(sum(greatest(c.reltuples, 0)), 0)::bigint FROM pg_inherits i \
         JOIN pg_class c ON c.oid = i.inhrelid WHERE i.inhparent = $1::regclass",
    )
    .bind(table)
    .fetch_one(conn)
    .await?;
    Ok(n)
}

// ---------------------------------------------------------------------------
// users
// ---------------------------------------------------------------------------

pub async fn insert_user(conn: &mut PgConnection, id: i64, u: &UserInput) -> AppResult<User> {
    Ok(sqlx::query_as::<_, User>("INSERT INTO users (id, username, email) VALUES ($1, $2, $3) RETURNING id, username, email, created_at")
        .bind(id)
        .bind(&u.username)
        .bind(&u.email)
        .fetch_one(conn)
        .await?)
}

pub async fn get_user(conn: &mut PgConnection, id: i64) -> AppResult<Option<User>> {
    Ok(sqlx::query_as::<_, User>("SELECT id, username, email, created_at FROM users WHERE id = $1").bind(id).fetch_optional(conn).await?)
}

pub async fn usernames(conn: &mut PgConnection, ids: &[i64]) -> AppResult<HashMap<i64, String>> {
    if ids.is_empty() {
        return Ok(HashMap::new());
    }
    let rows: Vec<(i64, String)> = sqlx::query_as("SELECT id, username FROM users WHERE id = ANY($1)").bind(ids).fetch_all(conn).await?;
    Ok(rows.into_iter().collect())
}

// ---------------------------------------------------------------------------
// books
// ---------------------------------------------------------------------------

pub async fn get_book(conn: &mut PgConnection, id: i64, for_update: bool) -> AppResult<Option<BookRow>> {
    let sql = format!(
        "SELECT {BOOK_COLUMNS} FROM books b WHERE b.id = $1 AND b.deleted_at IS NULL{}",
        if for_update { " FOR UPDATE" } else { "" }
    );
    Ok(sqlx::query_as::<_, BookRow>(&sql).bind(id).fetch_optional(conn).await?)
}

/// True if the book exists at all (including soft-deleted) — used by history.
pub async fn book_exists_any(conn: &mut PgConnection, id: i64) -> AppResult<bool> {
    let r: Option<(i64,)> = sqlx::query_as("SELECT id FROM books WHERE id = $1").bind(id).fetch_optional(conn).await?;
    Ok(r.is_some())
}

fn year(d: &BookData) -> Option<i16> {
    d.publication_year.and_then(|y| i16::try_from(y).ok())
}

pub async fn insert_book(conn: &mut PgConnection, id: i64, d: &BookData, user: i64) -> AppResult<BookRow> {
    let sql = format!(
        "INSERT INTO books (id, title, original_title, subtitle, description, isbn_10, isbn_13, language_code, \
         publication_year, publisher, page_count, genres, edition, external_ids, created_by, updated_by) \
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $15) RETURNING {}",
        returning(BOOK_COLUMNS, "b.")
    );
    Ok(sqlx::query_as::<_, BookRow>(&sql)
        .bind(id)
        .bind(&d.title)
        .bind(&d.original_title)
        .bind(&d.subtitle)
        .bind(&d.description)
        .bind(&d.isbn_10)
        .bind(&d.isbn_13)
        .bind(&d.language_code)
        .bind(year(d))
        .bind(&d.publisher)
        .bind(d.page_count)
        .bind(&d.genres)
        .bind(&d.edition)
        .bind(&d.external_ids)
        .bind(user)
        .fetch_one(conn)
        .await?)
}

pub async fn update_book(conn: &mut PgConnection, id: i64, d: &BookData, user: i64, version: i32) -> AppResult<Option<BookRow>> {
    let sql = format!(
        "UPDATE books SET title = $2, original_title = $3, subtitle = $4, description = $5, isbn_10 = $6, \
         isbn_13 = $7, language_code = $8, publication_year = $9, publisher = $10, page_count = $11, genres = $12, \
         edition = $13, external_ids = $14, updated_by = $15, updated_at = now(), version = version + 1 \
         WHERE id = $1 AND version = $16 AND deleted_at IS NULL RETURNING {}",
        returning(BOOK_COLUMNS, "b.")
    );
    Ok(sqlx::query_as::<_, BookRow>(&sql)
        .bind(id)
        .bind(&d.title)
        .bind(&d.original_title)
        .bind(&d.subtitle)
        .bind(&d.description)
        .bind(&d.isbn_10)
        .bind(&d.isbn_13)
        .bind(&d.language_code)
        .bind(year(d))
        .bind(&d.publisher)
        .bind(d.page_count)
        .bind(&d.genres)
        .bind(&d.edition)
        .bind(&d.external_ids)
        .bind(user)
        .bind(version)
        .fetch_optional(conn)
        .await?)
}

pub async fn soft_delete_book(conn: &mut PgConnection, id: i64, user: i64) -> AppResult<Option<BookRow>> {
    let sql = format!(
        "UPDATE books SET deleted_at = now(), updated_at = now(), updated_by = $2, version = version + 1 \
         WHERE id = $1 AND deleted_at IS NULL RETURNING {}",
        returning(BOOK_COLUMNS, "b.")
    );
    Ok(sqlx::query_as::<_, BookRow>(&sql).bind(id).bind(user).fetch_optional(conn).await?)
}

pub async fn register_isbn(conn: &mut PgConnection, isbn13: &str, book_id: i64) -> AppResult<()> {
    sqlx::query("INSERT INTO isbn_registry (isbn_13, book_id) VALUES ($1, $2)").bind(isbn13).bind(book_id).execute(conn).await?;
    Ok(())
}

pub async fn release_isbn(conn: &mut PgConnection, isbn13: &str, book_id: i64) -> AppResult<()> {
    sqlx::query("DELETE FROM isbn_registry WHERE isbn_13 = $1 AND book_id = $2").bind(isbn13).bind(book_id).execute(conn).await?;
    Ok(())
}

/// Written as nested-loop LATERAL lookups on purpose: each probe is `book_id = x.id` /
/// `id = ba.author_id`, i.e. a single-partition index lookup with run-time pruning, in both
/// custom and generic plans. The naive `JOIN ... WHERE ba.book_id = ANY($1)` gets a generic
/// plan that hashes the whole authors table (found under load, see WORKLOG.md).
pub const SQL_AUTHORS_OF_BOOKS: &str = "SELECT ba.book_id, ba.author_id, a.full_name, ba.role, ba.ordinal \
    FROM unnest($1::bigint[]) AS x(id) \
    CROSS JOIN LATERAL (SELECT * FROM book_authors b2 WHERE b2.book_id = x.id) ba \
    CROSS JOIN LATERAL (SELECT a2.full_name FROM authors a2 WHERE a2.id = ba.author_id AND a2.deleted_at IS NULL LIMIT 1) a \
    ORDER BY ba.book_id, ba.ordinal, ba.author_id, ba.role";

/// Authors (live only) of the given books, for display.
pub async fn authors_of_books(conn: &mut PgConnection, book_ids: &[i64]) -> AppResult<Vec<BookAuthor>> {
    if book_ids.is_empty() {
        return Ok(Vec::new());
    }
    Ok(sqlx::query_as::<_, BookAuthor>(SQL_AUTHORS_OF_BOOKS).bind(book_ids).fetch_all(conn).await?)
}

/// Raw links of one book (including links to soft-deleted authors).
pub async fn links_of_book(conn: &mut PgConnection, book_id: i64) -> AppResult<Vec<AuthorLink>> {
    let rows: Vec<(i64, String, i16)> =
        sqlx::query_as("SELECT author_id, role, ordinal FROM book_authors WHERE book_id = $1 ORDER BY ordinal, author_id, role")
            .bind(book_id)
            .fetch_all(conn)
            .await?;
    Ok(rows.into_iter().map(|(author_id, role, ordinal)| AuthorLink { author_id, role, ordinal: Some(ordinal) }).collect())
}

/// Replaces the links of a book in both directions (book_authors + author_books).
pub async fn replace_links(conn: &mut PgConnection, book_id: i64, old: &[AuthorLink], new: &[AuthorLink]) -> AppResult<()> {
    if !old.is_empty() {
        sqlx::query("DELETE FROM book_authors WHERE book_id = $1").bind(book_id).execute(&mut *conn).await?;
        let old_ids: Vec<i64> = old.iter().map(|l| l.author_id).collect();
        sqlx::query("DELETE FROM author_books WHERE author_id = ANY($1) AND book_id = $2")
            .bind(&old_ids)
            .bind(book_id)
            .execute(&mut *conn)
            .await?;
    }
    if new.is_empty() {
        return Ok(());
    }
    let ids: Vec<i64> = new.iter().map(|l| l.author_id).collect();
    let roles: Vec<String> = new.iter().map(|l| l.role.clone()).collect();
    let ords: Vec<i16> = new.iter().map(|l| l.ordinal.unwrap_or(1)).collect();
    sqlx::query(
        "INSERT INTO book_authors (book_id, author_id, role, ordinal) \
         SELECT $1, t.a, t.r, t.o FROM UNNEST($2::bigint[], $3::text[], $4::smallint[]) AS t(a, r, o)",
    )
    .bind(book_id)
    .bind(&ids)
    .bind(&roles)
    .bind(&ords)
    .execute(&mut *conn)
    .await?;
    sqlx::query(
        "INSERT INTO author_books (author_id, book_id, role, ordinal) \
         SELECT t.a, $1, t.r, t.o FROM UNNEST($2::bigint[], $3::text[], $4::smallint[]) AS t(a, r, o)",
    )
    .bind(book_id)
    .bind(&ids)
    .bind(&roles)
    .bind(&ords)
    .execute(&mut *conn)
    .await?;
    Ok(())
}

pub fn sql_books_of_author() -> String {
    format!(
        "SELECT {BOOK_COLUMNS} FROM (SELECT DISTINCT ab.book_id FROM author_books ab \
         WHERE ab.author_id = $1 AND ab.book_id > $2) x \
         JOIN books b ON b.id = x.book_id WHERE b.deleted_at IS NULL ORDER BY x.book_id LIMIT $3"
    )
}

/// Books of an author ordered by book id (keyset on book id).
pub async fn books_of_author(conn: &mut PgConnection, author_id: i64, after: i64, limit: i64) -> AppResult<Vec<BookRow>> {
    Ok(sqlx::query_as::<_, BookRow>(&sql_books_of_author()).bind(author_id).bind(after).bind(limit).fetch_all(conn).await?)
}

// ---------------------------------------------------------------------------
// authors
// ---------------------------------------------------------------------------

pub async fn get_author(conn: &mut PgConnection, id: i64, for_update: bool) -> AppResult<Option<AuthorRow>> {
    let sql = format!(
        "SELECT {AUTHOR_COLUMNS} FROM authors a WHERE a.id = $1 AND a.deleted_at IS NULL{}",
        if for_update { " FOR UPDATE" } else { "" }
    );
    Ok(sqlx::query_as::<_, AuthorRow>(&sql).bind(id).fetch_optional(conn).await?)
}

pub async fn author_exists_any(conn: &mut PgConnection, id: i64) -> AppResult<bool> {
    let r: Option<(i64,)> = sqlx::query_as("SELECT id FROM authors WHERE id = $1").bind(id).fetch_optional(conn).await?;
    Ok(r.is_some())
}

pub async fn live_author_ids(conn: &mut PgConnection, ids: &[i64]) -> AppResult<Vec<i64>> {
    if ids.is_empty() {
        return Ok(Vec::new());
    }
    let rows: Vec<(i64,)> =
        sqlx::query_as("SELECT id FROM authors WHERE id = ANY($1) AND deleted_at IS NULL").bind(ids).fetch_all(conn).await?;
    Ok(rows.into_iter().map(|r| r.0).collect())
}

pub async fn insert_author(conn: &mut PgConnection, id: i64, d: &AuthorData, user: i64) -> AppResult<AuthorRow> {
    let sql = format!(
        "INSERT INTO authors (id, full_name, original_name, birth_date, death_date, country_code, biography, \
         external_ids, created_by, updated_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9) RETURNING {}",
        returning(AUTHOR_COLUMNS, "a.")
    );
    Ok(sqlx::query_as::<_, AuthorRow>(&sql)
        .bind(id)
        .bind(&d.full_name)
        .bind(&d.original_name)
        .bind(d.birth_date)
        .bind(d.death_date)
        .bind(&d.country_code)
        .bind(&d.biography)
        .bind(&d.external_ids)
        .bind(user)
        .fetch_one(conn)
        .await?)
}

pub async fn update_author(conn: &mut PgConnection, id: i64, d: &AuthorData, user: i64, version: i32) -> AppResult<Option<AuthorRow>> {
    let sql = format!(
        "UPDATE authors SET full_name = $2, original_name = $3, birth_date = $4, death_date = $5, country_code = $6, \
         biography = $7, external_ids = $8, updated_by = $9, updated_at = now(), version = version + 1 \
         WHERE id = $1 AND version = $10 AND deleted_at IS NULL RETURNING {}",
        returning(AUTHOR_COLUMNS, "a.")
    );
    Ok(sqlx::query_as::<_, AuthorRow>(&sql)
        .bind(id)
        .bind(&d.full_name)
        .bind(&d.original_name)
        .bind(d.birth_date)
        .bind(d.death_date)
        .bind(&d.country_code)
        .bind(&d.biography)
        .bind(&d.external_ids)
        .bind(user)
        .bind(version)
        .fetch_optional(conn)
        .await?)
}

pub async fn soft_delete_author(conn: &mut PgConnection, id: i64, user: i64) -> AppResult<Option<AuthorRow>> {
    let sql = format!(
        "UPDATE authors SET deleted_at = now(), updated_at = now(), updated_by = $2, version = version + 1 \
         WHERE id = $1 AND deleted_at IS NULL RETURNING {}",
        returning(AUTHOR_COLUMNS, "a.")
    );
    Ok(sqlx::query_as::<_, AuthorRow>(&sql).bind(id).bind(user).fetch_optional(conn).await?)
}

// ---------------------------------------------------------------------------
// audit
// ---------------------------------------------------------------------------

pub struct NewAudit<'a> {
    pub id: i64,
    pub entity_type: &'a str,
    pub entity_id: i64,
    pub action: &'a str,
    pub changed_by: i64,
    pub diff: Value,
}

pub async fn insert_audit(conn: &mut PgConnection, a: NewAudit<'_>) -> AppResult<()> {
    sqlx::query(
        "INSERT INTO audit_log (id, entity_type, entity_id, action, changed_by, changed_at, diff) \
         VALUES ($1, $2, $3, $4, $5, now(), $6)",
    )
    .bind(a.id)
    .bind(a.entity_type)
    .bind(a.entity_id)
    .bind(a.action)
    .bind(a.changed_by)
    .bind(&a.diff)
    .execute(conn)
    .await?;
    Ok(())
}

pub const SQL_AUDIT_HISTORY: &str = "SELECT id, entity_type, entity_id, action, changed_by, changed_at, diff FROM audit_log \
    WHERE entity_type = $1 AND entity_id = $2 \
      AND ($3::timestamptz IS NULL OR (changed_at, id) > ($3::timestamptz, $4::bigint)) \
    ORDER BY changed_at, id LIMIT $5";

pub async fn audit_history(
    conn: &mut PgConnection,
    entity_type: &str,
    entity_id: i64,
    after: Option<(DateTime<Utc>, i64)>,
    limit: i64,
) -> AppResult<Vec<AuditRow>> {
    let (ts, id) = after.map_or((None, None), |(t, i)| (Some(t), Some(i)));
    Ok(sqlx::query_as::<_, AuditRow>(SQL_AUDIT_HISTORY)
        .bind(entity_type)
        .bind(entity_id)
        .bind(ts)
        .bind(id)
        .bind(limit)
        .fetch_all(conn)
        .await?)
}

// ---------------------------------------------------------------------------
// search
// ---------------------------------------------------------------------------

pub async fn search(conn: &mut PgConnection, sql: &str, q: &str, limit: i64, after: Option<(f64, i32, i64)>) -> AppResult<Vec<SearchHit>> {
    let (r, k, i) = after.map_or((None, None, None), |(r, k, i)| (Some(r), Some(k), Some(i)));
    Ok(sqlx::query_as::<_, SearchHit>(sql).bind(q).bind(limit).bind(r).bind(k).bind(i).fetch_all(conn).await?)
}

pub async fn search_estimate(conn: &mut PgConnection, sql: &str, q: &str) -> AppResult<Option<i64>> {
    let (plan,): (Value,) = sqlx::query_as(sql).bind(q).fetch_one(conn).await?;
    Ok(query::plan_rows(&plan))
}
