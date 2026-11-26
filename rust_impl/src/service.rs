//! Service layer: validation, transactions, optimistic locking, audit trail and
//! response enrichment. Handlers call these; these call the repository.

use std::{collections::HashMap, sync::Arc};

use serde_json::Value;
use sqlx::PgConnection;

use crate::{
    cursor::Cursor,
    db::Db,
    error::{AppError, AppResult, FieldError},
    ids::IdGenerator,
    models::{
        AuditEntry, Author, AuthorData, AuthorInput, AuthorPatch, AuthorRow, Book, BookAuthor, BookData, BookInput, BookPatch, BookRow,
        ListResponse, SearchHit, User, UserInput, UserRef,
    },
    query::{
        self, AuthorListQuery, BookListQuery, PageReq, SearchMode, SearchQuery, build_author_estimate, build_author_list,
        build_book_estimate, build_book_list,
    },
    repository::{self as repo, NewAudit},
    validation,
};

#[derive(Clone)]
pub struct Service {
    pub db: Db,
    pub ids: Arc<IdGenerator>,
}

/// Expected version for a write, from If-Match or the body's `version`.
fn require_version(if_match: Option<i32>, body: Option<i32>) -> AppResult<i32> {
    if_match
        .or(body)
        .ok_or_else(|| AppError::PreconditionRequired("send the current version in the If-Match header (or `version` in the body)".into()))
}

fn version_conflict(current: i32, expected: i32) -> AppError {
    AppError::Conflict(format!("version mismatch: current version is {current}, you sent {expected}; reload and retry"))
}

async fn user_refs(conn: &mut PgConnection, ids: impl Iterator<Item = i64>) -> AppResult<HashMap<i64, String>> {
    let mut v: Vec<i64> = ids.collect();
    v.sort_unstable();
    v.dedup();
    repo::usernames(conn, &v).await
}

fn user_ref(map: &HashMap<i64, String>, id: i64) -> UserRef {
    UserRef { id, username: map.get(&id).cloned() }
}

impl Service {
    pub fn new(db: Db, ids: Arc<IdGenerator>) -> Self {
        Self { db, ids }
    }

    async fn audit(
        &self,
        conn: &mut PgConnection,
        entity_type: &str,
        entity_id: i64,
        action: &str,
        user: i64,
        diff: Value,
    ) -> AppResult<()> {
        repo::insert_audit(conn, NewAudit { id: self.ids.next_id(), entity_type, entity_id, action, changed_by: user, diff }).await
    }

    // -----------------------------------------------------------------------
    // users
    // -----------------------------------------------------------------------

    pub async fn create_user(&self, mut input: UserInput, actor: Option<i64>) -> AppResult<User> {
        validation::validate_user(&mut input)?;
        let mut tx = self.db.begin().await?;
        let id = self.ids.next_id();
        let user = repo::insert_user(&mut tx, id, &input).await?;
        let diff = serde_json::json!({"username": user.username, "email": user.email});
        self.audit(&mut tx, "user", id, "create", actor.unwrap_or(id), diff).await?;
        tx.commit().await?;
        Ok(user)
    }

    pub async fn get_user(&self, id: i64) -> AppResult<User> {
        let mut tx = self.db.begin_read().await?;
        let u = repo::get_user(&mut tx, id).await?.ok_or_else(|| AppError::NotFound(format!("user {id} not found")))?;
        tx.commit().await?;
        Ok(u)
    }

    // -----------------------------------------------------------------------
    // books
    // -----------------------------------------------------------------------

    async fn enrich_books(&self, conn: &mut PgConnection, rows: Vec<BookRow>) -> AppResult<Vec<Book>> {
        let ids: Vec<i64> = rows.iter().map(|r| r.id).collect();
        let mut by_book: HashMap<i64, Vec<BookAuthor>> = HashMap::new();
        for a in repo::authors_of_books(conn, &ids).await? {
            by_book.entry(a.book_id).or_default().push(a);
        }
        let users = user_refs(conn, rows.iter().flat_map(|r| [r.created_by, r.updated_by])).await?;
        Ok(rows
            .into_iter()
            .map(|r| {
                let authors = by_book.remove(&r.id).unwrap_or_default();
                let (c, u) = (user_ref(&users, r.created_by), user_ref(&users, r.updated_by));
                r.into_book(authors, c, u)
            })
            .collect())
    }

    async fn check_authors_exist(&self, conn: &mut PgConnection, d: &BookData) -> AppResult<()> {
        let ids: Vec<i64> = d.authors.iter().map(|a| a.author_id).collect();
        let live = repo::live_author_ids(conn, &ids).await?;
        let missing: Vec<FieldError> = d
            .authors
            .iter()
            .enumerate()
            .filter(|(_, a)| !live.contains(&a.author_id))
            .map(|(i, a)| FieldError::new(format!("authors[{i}].author_id"), format!("author {} does not exist", a.author_id)))
            .collect();
        if missing.is_empty() { Ok(()) } else { Err(AppError::Validation(missing)) }
    }

    pub async fn list_books(&self, q: BookListQuery) -> AppResult<ListResponse<Book>> {
        let mut tx = self.db.begin_read().await?;
        let mut rows = repo::fetch_books(&mut tx, build_book_list(&q)?).await?;
        let limit = q.page.limit as usize;
        let next = if rows.len() > limit {
            rows.truncate(limit);
            rows.last().map(|r| q.cursor_after(r))
        } else {
            None
        };
        let total = if q.page.with_total {
            if q.filter.is_empty() {
                Some(repo::reltuples(&mut tx, "books").await?)
            } else {
                repo::explain_estimate(&mut tx, build_book_estimate(&q.filter)).await?
            }
        } else {
            None
        };
        let items = self.enrich_books(&mut tx, rows).await?;
        tx.commit().await?;
        Ok(ListResponse { items, next_cursor: next, limit: q.page.limit, total, total_is_estimate: total.is_some() })
    }

    pub async fn get_book(&self, id: i64) -> AppResult<Book> {
        let mut tx = self.db.begin_read().await?;
        let row = repo::get_book(&mut tx, id, false).await?.ok_or_else(|| book_not_found(id))?;
        let mut v = self.enrich_books(&mut tx, vec![row]).await?;
        tx.commit().await?;
        v.pop().ok_or_else(|| book_not_found(id))
    }

    pub async fn create_book(&self, input: BookInput, user: i64) -> AppResult<Book> {
        let mut d = input.into_data();
        validation::validate_book(&mut d)?;
        let mut tx = self.db.begin().await?;
        self.check_authors_exist(&mut tx, &d).await?;
        let id = self.ids.next_id();
        let row = repo::insert_book(&mut tx, id, &d, user).await?;
        if let Some(i) = &d.isbn_13 {
            repo::register_isbn(&mut tx, i, id).await?;
        }
        repo::replace_links(&mut tx, id, &[], &d.authors).await?;
        self.audit(&mut tx, "book", id, "create", user, validation::snapshot(&d)).await?;
        let mut v = self.enrich_books(&mut tx, vec![row]).await?;
        tx.commit().await?;
        v.pop().ok_or_else(|| AppError::Internal("created book vanished".into()))
    }

    async fn write_book(&self, id: i64, user: i64, expected: i32, build: impl FnOnce(BookData) -> AppResult<BookData>) -> AppResult<Book> {
        let mut tx = self.db.begin().await?;
        let cur = repo::get_book(&mut tx, id, true).await?.ok_or_else(|| book_not_found(id))?;
        if cur.version != expected {
            return Err(version_conflict(cur.version, expected));
        }
        let links = repo::links_of_book(&mut tx, id).await?;
        let old = cur.data(&links);
        let mut new = build(old.clone())?;
        validation::validate_book(&mut new)?;
        if new.authors != old.authors {
            self.check_authors_exist(&mut tx, &new).await?;
        }
        let row = repo::update_book(&mut tx, id, &new, user, expected).await?.ok_or_else(|| version_conflict(cur.version + 1, expected))?;
        if new.isbn_13 != old.isbn_13 {
            if let Some(i) = &old.isbn_13 {
                repo::release_isbn(&mut tx, i, id).await?;
            }
            if let Some(i) = &new.isbn_13 {
                repo::register_isbn(&mut tx, i, id).await?;
            }
        }
        if new.authors != old.authors {
            repo::replace_links(&mut tx, id, &old.authors, &new.authors).await?;
        }
        self.audit(&mut tx, "book", id, "update", user, validation::diff(&old, &new)).await?;
        let mut v = self.enrich_books(&mut tx, vec![row]).await?;
        tx.commit().await?;
        v.pop().ok_or_else(|| book_not_found(id))
    }

    pub async fn replace_book(&self, id: i64, input: BookInput, user: i64, if_match: Option<i32>) -> AppResult<Book> {
        let expected = require_version(if_match, input.version)?;
        self.write_book(id, user, expected, |_| Ok(input.into_data())).await
    }

    pub async fn patch_book(&self, id: i64, patch: BookPatch, user: i64, if_match: Option<i32>) -> AppResult<Book> {
        let expected = require_version(if_match, patch.version)?;
        self.write_book(id, user, expected, |old| validation::apply_book_patch(old, patch)).await
    }

    pub async fn delete_book(&self, id: i64, user: i64, if_match: Option<i32>) -> AppResult<()> {
        let mut tx = self.db.begin().await?;
        let cur = repo::get_book(&mut tx, id, true).await?.ok_or_else(|| book_not_found(id))?;
        if let Some(v) = if_match {
            if v != cur.version {
                return Err(version_conflict(cur.version, v));
            }
        }
        repo::soft_delete_book(&mut tx, id, user).await?.ok_or_else(|| book_not_found(id))?;
        if let Some(i) = &cur.isbn_13 {
            repo::release_isbn(&mut tx, i, id).await?;
        }
        self.audit(&mut tx, "book", id, "delete", user, serde_json::json!({"deleted_at": {"old": null, "new": "now"}})).await?;
        tx.commit().await?;
        Ok(())
    }

    // -----------------------------------------------------------------------
    // history
    // -----------------------------------------------------------------------

    pub async fn history(&self, entity_type: &str, id: i64, page: PageReq) -> AppResult<ListResponse<AuditEntry>> {
        let mut tx = self.db.begin_read().await?;
        let exists = match entity_type {
            "book" => repo::book_exists_any(&mut tx, id).await?,
            _ => repo::author_exists_any(&mut tx, id).await?,
        };
        if !exists {
            return Err(AppError::NotFound(format!("{entity_type} {id} not found")));
        }
        let after = page.cursor.as_ref().map(query::history_cursor_value).transpose()?;
        let mut rows = repo::audit_history(&mut tx, entity_type, id, after, i64::from(page.limit) + 1).await?;
        let next = if rows.len() > page.limit as usize {
            rows.truncate(page.limit as usize);
            rows.last()
                .map(|r| Cursor::new(history_key(entity_type), false, Value::String(query::ts_to_cursor(&r.changed_at)), r.id).encode())
        } else {
            None
        };
        let users = user_refs(&mut tx, rows.iter().map(|r| r.changed_by)).await?;
        tx.commit().await?;
        let items = rows
            .into_iter()
            .map(|r| AuditEntry {
                id: r.id,
                entity_type: r.entity_type,
                entity_id: r.entity_id,
                action: r.action,
                changed_by: user_ref(&users, r.changed_by),
                changed_at: r.changed_at,
                diff: r.diff,
            })
            .collect();
        Ok(ListResponse { items, next_cursor: next, limit: page.limit, total: None, total_is_estimate: false })
    }

    // -----------------------------------------------------------------------
    // authors
    // -----------------------------------------------------------------------

    async fn enrich_authors(&self, conn: &mut PgConnection, rows: Vec<AuthorRow>) -> AppResult<Vec<Author>> {
        let users = user_refs(conn, rows.iter().flat_map(|r| [r.created_by, r.updated_by])).await?;
        Ok(rows
            .into_iter()
            .map(|r| {
                let (c, u) = (user_ref(&users, r.created_by), user_ref(&users, r.updated_by));
                r.into_author(c, u)
            })
            .collect())
    }

    pub async fn list_authors(&self, q: AuthorListQuery) -> AppResult<ListResponse<Author>> {
        let mut tx = self.db.begin_read().await?;
        let mut rows = repo::fetch_authors(&mut tx, build_author_list(&q)?).await?;
        let limit = q.page.limit as usize;
        let next = if rows.len() > limit {
            rows.truncate(limit);
            rows.last().map(|r| q.cursor_after(r))
        } else {
            None
        };
        let total = if q.page.with_total {
            if q.filter.is_empty() {
                Some(repo::reltuples(&mut tx, "authors").await?)
            } else {
                repo::explain_estimate(&mut tx, build_author_estimate(&q.filter)).await?
            }
        } else {
            None
        };
        let items = self.enrich_authors(&mut tx, rows).await?;
        tx.commit().await?;
        Ok(ListResponse { items, next_cursor: next, limit: q.page.limit, total, total_is_estimate: total.is_some() })
    }

    pub async fn get_author(&self, id: i64) -> AppResult<Author> {
        let mut tx = self.db.begin_read().await?;
        let row = repo::get_author(&mut tx, id, false).await?.ok_or_else(|| author_not_found(id))?;
        let mut v = self.enrich_authors(&mut tx, vec![row]).await?;
        tx.commit().await?;
        v.pop().ok_or_else(|| author_not_found(id))
    }

    pub async fn books_of_author(&self, id: i64, page: PageReq) -> AppResult<ListResponse<Book>> {
        let mut tx = self.db.begin_read().await?;
        if repo::get_author(&mut tx, id, false).await?.is_none() {
            return Err(author_not_found(id));
        }
        let after = page.cursor.as_ref().and_then(Cursor::id_value).unwrap_or(0);
        let mut rows = repo::books_of_author(&mut tx, id, after, i64::from(page.limit) + 1).await?;
        let next = if rows.len() > page.limit as usize {
            rows.truncate(page.limit as usize);
            rows.last().map(|r| Cursor::new(AUTHOR_BOOKS_KEY, false, Value::Null, r.id).encode())
        } else {
            None
        };
        let items = self.enrich_books(&mut tx, rows).await?;
        tx.commit().await?;
        Ok(ListResponse { items, next_cursor: next, limit: page.limit, total: None, total_is_estimate: false })
    }

    pub async fn create_author(&self, input: AuthorInput, user: i64) -> AppResult<Author> {
        let mut d = input.into_data();
        validation::validate_author(&mut d)?;
        let mut tx = self.db.begin().await?;
        let id = self.ids.next_id();
        let row = repo::insert_author(&mut tx, id, &d, user).await?;
        self.audit(&mut tx, "author", id, "create", user, validation::snapshot(&d)).await?;
        let mut v = self.enrich_authors(&mut tx, vec![row]).await?;
        tx.commit().await?;
        v.pop().ok_or_else(|| AppError::Internal("created author vanished".into()))
    }

    async fn write_author(
        &self,
        id: i64,
        user: i64,
        expected: i32,
        build: impl FnOnce(AuthorData) -> AppResult<AuthorData>,
    ) -> AppResult<Author> {
        let mut tx = self.db.begin().await?;
        let cur = repo::get_author(&mut tx, id, true).await?.ok_or_else(|| author_not_found(id))?;
        if cur.version != expected {
            return Err(version_conflict(cur.version, expected));
        }
        let old = cur.data();
        let mut new = build(old.clone())?;
        validation::validate_author(&mut new)?;
        let row =
            repo::update_author(&mut tx, id, &new, user, expected).await?.ok_or_else(|| version_conflict(cur.version + 1, expected))?;
        self.audit(&mut tx, "author", id, "update", user, validation::diff(&old, &new)).await?;
        let mut v = self.enrich_authors(&mut tx, vec![row]).await?;
        tx.commit().await?;
        v.pop().ok_or_else(|| author_not_found(id))
    }

    pub async fn replace_author(&self, id: i64, input: AuthorInput, user: i64, if_match: Option<i32>) -> AppResult<Author> {
        let expected = require_version(if_match, input.version)?;
        self.write_author(id, user, expected, |_| Ok(input.into_data())).await
    }

    pub async fn patch_author(&self, id: i64, patch: AuthorPatch, user: i64, if_match: Option<i32>) -> AppResult<Author> {
        let expected = require_version(if_match, patch.version)?;
        self.write_author(id, user, expected, |old| validation::apply_author_patch(old, patch)).await
    }

    pub async fn delete_author(&self, id: i64, user: i64, if_match: Option<i32>) -> AppResult<()> {
        let mut tx = self.db.begin().await?;
        let cur = repo::get_author(&mut tx, id, true).await?.ok_or_else(|| author_not_found(id))?;
        if let Some(v) = if_match {
            if v != cur.version {
                return Err(version_conflict(cur.version, v));
            }
        }
        repo::soft_delete_author(&mut tx, id, user).await?.ok_or_else(|| author_not_found(id))?;
        self.audit(&mut tx, "author", id, "delete", user, serde_json::json!({"deleted_at": {"old": null, "new": "now"}})).await?;
        tx.commit().await?;
        Ok(())
    }

    // -----------------------------------------------------------------------
    // search
    // -----------------------------------------------------------------------

    pub async fn search(&self, q: SearchQuery) -> AppResult<ListResponse<SearchHit>> {
        let mut tx = self.db.begin_read().await?;
        let after = q.page.cursor.as_ref().map(SearchQuery::cursor_parts).transpose()?;
        // Full text first; only if it finds nothing at all (typo, partial word) fall back to fuzzy
        // trigram matching. The mode is stored in the cursor so later pages stay consistent.
        let limit = i64::from(q.page.limit) + 1;
        let (mode, mut hits) = match after {
            Some((r, k, i, mode)) => (mode, repo::search(&mut tx, &query::search_sql(q.kind, mode), &q.q, limit, Some((r, k, i))).await?),
            None => {
                let hits = repo::search(&mut tx, &query::search_sql(q.kind, SearchMode::FullText), &q.q, limit, None).await?;
                if hits.is_empty() {
                    (SearchMode::Fuzzy, repo::search(&mut tx, &query::search_sql(q.kind, SearchMode::Fuzzy), &q.q, limit, None).await?)
                } else {
                    (SearchMode::FullText, hits)
                }
            }
        };
        let next = if hits.len() > q.page.limit as usize {
            hits.truncate(q.page.limit as usize);
            hits.last().map(|h| q.cursor_after(h, mode))
        } else {
            None
        };
        let total =
            if q.page.with_total { repo::search_estimate(&mut tx, &query::search_estimate_sql(q.kind, mode), &q.q).await? } else { None };
        tx.commit().await?;
        Ok(ListResponse { items: hits, next_cursor: next, limit: q.page.limit, total, total_is_estimate: total.is_some() })
    }
}

pub const AUTHOR_BOOKS_KEY: &str = "author_books:book_id";

pub fn history_key(entity_type: &str) -> String {
    format!("history:{entity_type}")
}

fn book_not_found(id: i64) -> AppError {
    AppError::NotFound(format!("book {id} not found"))
}

fn author_not_found(id: i64) -> AppError {
    AppError::NotFound(format!("author {id} not found"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn version_rules() {
        assert_eq!(require_version(Some(3), Some(1)).unwrap(), 3);
        assert_eq!(require_version(None, Some(2)).unwrap(), 2);
        assert_eq!(require_version(None, None).unwrap_err().status().as_u16(), 428);
        assert_eq!(version_conflict(2, 1).status().as_u16(), 409);
    }

    #[test]
    fn refs() {
        let m: HashMap<i64, String> = [(1, "system".to_string())].into_iter().collect();
        assert_eq!(user_ref(&m, 1).username.as_deref(), Some("system"));
        assert_eq!(user_ref(&m, 2).username, None);
        assert_eq!(history_key("book"), "history:book");
    }
}
