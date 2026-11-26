//! Query-plan checker: runs `EXPLAIN (ANALYZE, BUFFERS)` for the key API queries on a
//! seeded database, fails if any plan contains a Seq Scan on a large table, and writes
//! a Markdown report (docs/QUERY_PLANS.md).

use std::fmt::Write as _;

use anyhow::Context;
use serde_json::Value;
use sqlx::{PgPool, Postgres, QueryBuilder, postgres::PgPoolOptions};

use crate::{
    db::connect_options,
    models::BookRow,
    query::{self, AuthorListQuery, BookListQuery, Params, SearchMode, SearchType},
    repository,
};

/// Tables that must never be read with a sequential scan (parents and partitions).
pub const BIG_TABLES: &[&str] = &["books", "authors", "book_authors", "author_books", "isbn_registry", "audit_log"];

#[derive(Clone, Debug)]
pub enum Arg {
    I64(i64),
    Str(String),
    I64Arr(Vec<i64>),
    NullF64,
    NullI32,
    NullI64,
    NullTs,
}

/// Turns raw SQL with `$n` placeholders into a QueryBuilder with the given prefix.
pub fn raw(prefix: &str, sql: &str, args: &[Arg]) -> QueryBuilder<'static, Postgres> {
    let mut qb = QueryBuilder::new(prefix.to_string());
    let bytes = sql.as_bytes();
    let mut i = 0;
    let mut start = 0;
    while i < bytes.len() {
        if bytes[i] == b'$' && i + 1 < bytes.len() && bytes[i + 1].is_ascii_digit() {
            qb.push(&sql[start..i]);
            let mut j = i + 1;
            while j < bytes.len() && bytes[j].is_ascii_digit() {
                j += 1;
            }
            let n: usize = sql[i + 1..j].parse().unwrap_or(1);
            match args.get(n - 1).cloned().unwrap_or(Arg::NullI64) {
                Arg::I64(v) => qb.push_bind(v),
                Arg::Str(v) => qb.push_bind(v),
                Arg::I64Arr(v) => qb.push_bind(v),
                Arg::NullF64 => qb.push_bind(Option::<f64>::None),
                Arg::NullI32 => qb.push_bind(Option::<i32>::None),
                Arg::NullI64 => qb.push_bind(Option::<i64>::None),
                Arg::NullTs => qb.push_bind(Option::<chrono::DateTime<chrono::Utc>>::None),
            };
            i = j;
            start = j;
        } else {
            i += 1;
        }
    }
    qb.push(&sql[start..]);
    qb
}

/// Collects `(node type, relation)` of every Seq Scan in a JSON plan on a big table.
pub fn seq_scans_on_big_tables(plan: &Value) -> Vec<String> {
    fn walk(v: &Value, out: &mut Vec<String>) {
        match v {
            Value::Object(m) => {
                if m.get("Node Type").and_then(Value::as_str) == Some("Seq Scan") {
                    let rel = m.get("Relation Name").and_then(Value::as_str).unwrap_or_default();
                    let base = rel.split("_p").next().unwrap_or(rel);
                    let base = if rel.starts_with("audit_log") { "audit_log" } else { base };
                    if BIG_TABLES.contains(&base) {
                        out.push(rel.to_string());
                    }
                }
                m.values().for_each(|x| walk(x, out));
            }
            Value::Array(a) => a.iter().for_each(|x| walk(x, out)),
            _ => {}
        }
    }
    let mut out = Vec::new();
    walk(plan, &mut out);
    out
}

type Builder = Box<dyn Fn(&str) -> anyhow::Result<QueryBuilder<'static, Postgres>> + Send + Sync>;

struct Case {
    name: String,
    endpoint: String,
    build: Builder,
}

fn params(pairs: &[(&str, String)]) -> Params {
    Params::new(pairs.iter().map(|(k, v)| ((*k).to_string(), v.clone())).collect())
}

fn book_case(name: &str, endpoint: &str, p: Params) -> anyhow::Result<Case> {
    let q = BookListQuery::parse(&p, 20).map_err(|e| anyhow::anyhow!("{e}"))?;
    Ok(Case {
        name: name.into(),
        endpoint: endpoint.into(),
        build: Box::new(move |prefix| query::build_book_list_prefixed(prefix, &q).map_err(|e| anyhow::anyhow!("{e}"))),
    })
}

fn author_case(name: &str, endpoint: &str, p: Params) -> anyhow::Result<Case> {
    let q = AuthorListQuery::parse(&p, 20).map_err(|e| anyhow::anyhow!("{e}"))?;
    Ok(Case {
        name: name.into(),
        endpoint: endpoint.into(),
        build: Box::new(move |prefix| query::build_author_list_prefixed(prefix, &q).map_err(|e| anyhow::anyhow!("{e}"))),
    })
}

fn raw_case(name: &str, endpoint: &str, sql: String, args: Vec<Arg>) -> Case {
    Case { name: name.into(), endpoint: endpoint.into(), build: Box::new(move |prefix| Ok(raw(prefix, &sql, &args))) }
}

async fn one<T>(pool: &PgPool, sql: &str) -> anyhow::Result<T>
where
    T: for<'r> sqlx::Decode<'r, Postgres> + sqlx::Type<Postgres> + Send + Unpin,
{
    let (v,): (T,) = sqlx::query_as(sql).fetch_one(pool).await.with_context(|| sql.to_string())?;
    Ok(v)
}

async fn build_cases(pool: &PgPool) -> anyhow::Result<Vec<Case>> {
    // sample values taken from the data itself
    let author_id: i64 =
        one(pool, "SELECT author_id FROM author_books GROUP BY author_id ORDER BY count(*) DESC, author_id LIMIT 1").await?;
    let isbn: String = one(pool, "SELECT isbn_13 FROM isbn_registry LIMIT 1").await?;
    let book_id: i64 = one(pool, "SELECT id FROM books WHERE deleted_at IS NULL LIMIT 1").await?;
    let publisher: String = one(pool, "SELECT publisher FROM books WHERE publisher IS NOT NULL LIMIT 1").await?;
    let created_by: i64 = one(pool, "SELECT created_by FROM books LIMIT 1").await?;
    let title_word: String =
        one(pool, "SELECT coalesce((SELECT split_part(title, ' ', 2) FROM books WHERE language_code = 'en' AND title LIKE 'The % %' LIMIT 1), 'garden')").await?;

    let s = |v: &str| v.to_string();
    // a second-page cursor for sort=title (keyset continuation)
    let first = BookListQuery::parse(&params(&[("sort", s("title"))]), 20).map_err(|e| anyhow::anyhow!("{e}"))?;
    let mut conn = pool.acquire().await?;
    let rows: Vec<BookRow> = repository::fetch_books(&mut conn, query::build_book_list(&first).map_err(|e| anyhow::anyhow!("{e}"))?)
        .await
        .map_err(|e| anyhow::anyhow!("{e}"))?;
    let title_cursor = rows.get(19).map(|r| first.cursor_after(r)).unwrap_or_default();
    let first_a = AuthorListQuery::parse(&params(&[("sort", s("full_name"))]), 20).map_err(|e| anyhow::anyhow!("{e}"))?;
    let arows = repository::fetch_authors(&mut conn, query::build_author_list(&first_a).map_err(|e| anyhow::anyhow!("{e}"))?)
        .await
        .map_err(|e| anyhow::anyhow!("{e}"))?;
    let name_cursor = arows.get(19).map(|r| first_a.cursor_after(r)).unwrap_or_default();

    let mut cases = vec![
        book_case("books: default sort (-created_at), first page", "GET /api/v1/books", params(&[]))?,
        book_case(
            "books: sort=title, second page (keyset)",
            "GET /api/v1/books?sort=title&cursor=…",
            params(&[("sort", s("title")), ("cursor", title_cursor)]),
        )?,
        book_case(
            "books: sort=-publication_year + year range",
            "GET /api/v1/books?sort=-publication_year&year_from=1990&year_to=2000",
            params(&[("sort", s("-publication_year")), ("year_from", s("1990")), ("year_to", s("2000"))]),
        )?,
        book_case(
            "books: language filter + sort=title",
            "GET /api/v1/books?language=ja&sort=title",
            params(&[("language", s("ja")), ("sort", s("title"))]),
        )?,
        book_case("books: genre filter", "GET /api/v1/books?genre=poetry", params(&[("genre", s("poetry"))]))?,
        book_case(
            "books: rare genre + language (combined GIN)",
            "GET /api/v1/books?genre=cooking&language=ar",
            params(&[("genre", s("cooking")), ("language", s("ar"))]),
        )?,
        book_case("books: publisher filter", "GET /api/v1/books?publisher=…", params(&[("publisher", publisher)]))?,
        book_case("books: created_by filter", "GET /api/v1/books?created_by=…", params(&[("created_by", created_by.to_string())]))?,
        book_case(
            "books: created_from/created_to",
            "GET /api/v1/books?created_from=2024-01-01&created_to=2024-01-08",
            params(&[("created_from", s("2024-01-01")), ("created_to", s("2024-01-08"))]),
        )?,
        book_case("books: title substring (trigram)", "GET /api/v1/books?title=…", params(&[("title", title_word.clone())]))?,
        book_case("books: q full-text", "GET /api/v1/books?q=…", params(&[("q", format!("{title_word} garden"))]))?,
        book_case("books: author name filter", "GET /api/v1/books?author=akhmatova", params(&[("author", s("akhmatova"))]))?,
        book_case("books: author_id filter", "GET /api/v1/books?author_id=…", params(&[("author_id", author_id.to_string())]))?,
        book_case("books: isbn filter", "GET /api/v1/books?isbn=…", params(&[("isbn", isbn)]))?,
        author_case("authors: default sort (-created_at)", "GET /api/v1/authors", params(&[]))?,
        author_case(
            "authors: sort=full_name, second page",
            "GET /api/v1/authors?sort=full_name&cursor=…",
            params(&[("sort", s("full_name")), ("cursor", name_cursor)]),
        )?,
        author_case("authors: country filter", "GET /api/v1/authors?country=JP", params(&[("country", s("JP"))]))?,
        author_case("authors: name substring (trigram)", "GET /api/v1/authors?name=tolst", params(&[("name", s("tolst"))]))?,
        author_case(
            "authors: born range, sort=birth_date",
            "GET /api/v1/authors?born_from=1800-01-01&born_to=1850-12-31&sort=birth_date",
            params(&[("born_from", s("1800-01-01")), ("born_to", s("1850-12-31")), ("sort", s("birth_date"))]),
        )?,
        author_case("authors: q full-text", "GET /api/v1/authors?q=murakami", params(&[("q", s("murakami"))]))?,
    ];
    for (name, kind, mode, q) in [
        ("search: type=all, full-text mode (common word)", SearchType::All, SearchMode::FullText, "garden"),
        ("search: type=all, full-text mode (rare name)", SearchType::All, SearchMode::FullText, "dostoevsky"),
        ("search: type=books, fuzzy mode (typo)", SearchType::Books, SearchMode::Fuzzy, "Silnet Gardn"),
        ("search: type=authors, full-text mode", SearchType::Authors, SearchMode::FullText, "Толстой"),
        ("search: type=authors, fuzzy mode (typo)", SearchType::Authors, SearchMode::Fuzzy, "Dostoevskiy"),
    ] {
        cases.push(raw_case(
            name,
            &format!("GET /api/v1/search?q={q}"),
            query::search_sql(kind, mode),
            vec![Arg::Str(q.to_string()), Arg::I64(21), Arg::NullF64, Arg::NullI32, Arg::NullI64],
        ));
    }
    cases.push(raw_case(
        "book card: by id",
        "GET /api/v1/books/{id}",
        format!("SELECT {} FROM books b WHERE b.id = $1 AND b.deleted_at IS NULL", query::BOOK_COLUMNS),
        vec![Arg::I64(book_id)],
    ));
    cases.push(raw_case(
        "book card: authors of books (page enrichment)",
        "GET /api/v1/books/{id}",
        repository::SQL_AUTHORS_OF_BOOKS.to_string(),
        vec![Arg::I64Arr(vec![book_id])],
    ));
    cases.push(raw_case(
        "author books",
        "GET /api/v1/authors/{id}/books",
        repository::sql_books_of_author(),
        vec![Arg::I64(author_id), Arg::I64(0), Arg::I64(21)],
    ));
    cases.push(raw_case(
        "history",
        "GET /api/v1/books/{id}/history",
        repository::SQL_AUDIT_HISTORY.to_string(),
        vec![Arg::Str("book".into()), Arg::I64(book_id), Arg::NullTs, Arg::NullI64, Arg::I64(21)],
    ));
    Ok(cases)
}

pub struct CaseResult {
    pub name: String,
    pub seq_scans: Vec<String>,
    pub exec_ms: f64,
}

/// CLI entry point: fails if any checked plan has a Seq Scan on a big table.
pub async fn run(url: &str, out: Option<&str>) -> anyhow::Result<()> {
    run_with(url, out, true).await.map(|_| ())
}

/// Runs all cases; with `strict` a Seq Scan is an error, otherwise it is only reported
/// (tiny test databases legitimately prefer sequential scans).
pub async fn run_with(url: &str, out: Option<&str>, strict: bool) -> anyhow::Result<Vec<CaseResult>> {
    let pool =
        PgPoolOptions::new().max_connections(2).connect_with(connect_options(url, 0)?.options([("statement_timeout", "60000")])).await?;
    let (books,): (i64,) = sqlx::query_as("SELECT coalesce(sum(greatest(c.reltuples,0)),0)::bigint FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid WHERE i.inhparent = 'books'::regclass").fetch_one(&pool).await?;
    let (authors,): (i64,) = sqlx::query_as("SELECT coalesce(sum(greatest(c.reltuples,0)),0)::bigint FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid WHERE i.inhparent = 'authors'::regclass").fetch_one(&pool).await?;
    let (version,): (String,) = sqlx::query_as("SHOW server_version").fetch_one(&pool).await?;
    anyhow::ensure!(books > 0, "database is not seeded (books reltuples = 0); run `make seed` first");

    let cases = build_cases(&pool).await?;
    let mut md = String::new();
    let _ = writeln!(md, "# Query plans (EXPLAIN ANALYZE, BUFFERS)\n");
    let _ = writeln!(
        md,
        "Generated by `world-library explain` (`make explain`) on a seeded database: ~{books} books, ~{authors} authors, PostgreSQL {version}.\n\
         Each query is the exact SQL produced by the application's query builders, executed with real parameter values \
         (custom plan, `EXPLAIN ANALYZE`) and additionally planned as a **generic plan** (`EXPLAIN (GENERIC_PLAN)`, what a \
         prepared statement may switch to after 5 executions). The check fails if either plan contains a `Seq Scan` on {} \
         (or their partitions), or if a list query uses OFFSET.\n",
        BIG_TABLES.join(", ")
    );
    let mut results = Vec::new();
    let mut summary =
        String::from("| # | Query | Endpoint | Execution, ms | Seq Scan on big tables (custom + generic plan) |\n|---|---|---|---:|---|\n");
    let mut details = String::new();
    for (i, c) in cases.iter().enumerate() {
        let mut qb = (c.build)("EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) ")?;
        let sql_text = (c.build)("")?.into_sql();
        anyhow::ensure!(!sql_text.to_uppercase().contains(" OFFSET "), "{}: OFFSET used", c.name);
        let (plan,): (Value,) = qb.build_query_as().fetch_one(&pool).await.with_context(|| c.name.clone())?;
        let seq = seq_scans_on_big_tables(&plan);
        let exec_ms = plan.get(0).and_then(|p| p.get("Execution Time")).and_then(Value::as_f64).unwrap_or(-1.0);
        // Generic plan (what a prepared statement may switch to after 5 executions):
        // EXPLAIN (GENERIC_PLAN) plans the text with unbound $n parameters (PostgreSQL 16+).
        let generic_sql = format!("EXPLAIN (GENERIC_PLAN, FORMAT JSON) {sql_text}");
        let generic: Value = match sqlx::raw_sql(&generic_sql).fetch_one(&pool).await {
            Ok(row) => sqlx::Row::try_get::<Value, _>(&row, 0)?,
            Err(e) => anyhow::bail!("{}: generic plan failed: {e}", c.name),
        };
        let generic_seq = seq_scans_on_big_tables(&generic);
        let seq: Vec<String> = seq.into_iter().chain(generic_seq.iter().map(|r| format!("{r} (generic plan)"))).collect();
        let mut tq = (c.build)("EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, SUMMARY ON) ")?;
        let lines: Vec<(String,)> = tq.build_query_as().fetch_all(&pool).await.with_context(|| c.name.clone())?;
        let verdict = if seq.is_empty() { "none ✅".to_string() } else { format!("**{}** ❌", seq.join(", ")) };
        let _ = writeln!(summary, "| {} | {} | `{}` | {:.2} | {} |", i + 1, c.name, c.endpoint, exec_ms, verdict);
        let _ = writeln!(details, "## {}. {}\n\nEndpoint: `{}`\n\n```sql\n{}\n```\n\n```text", i + 1, c.name, c.endpoint, sql_text);
        for (l,) in lines {
            let _ = writeln!(details, "{l}");
        }
        let _ = writeln!(details, "```\n");
        println!("[explain] {:<55} {:>9.2} ms  {}", c.name, exec_ms, if seq.is_empty() { "OK" } else { "SEQ SCAN!" });
        results.push(CaseResult { name: c.name.clone(), seq_scans: seq, exec_ms });
    }
    let failed: Vec<&CaseResult> = results.iter().filter(|r| !r.seq_scans.is_empty()).collect();
    let _ = writeln!(md, "## Summary\n\n{summary}");
    let _ = writeln!(
        md,
        "Result: **{}** ({} queries checked, {} with Seq Scan on big tables).\n",
        if failed.is_empty() { "PASS" } else { "FAIL" },
        results.len(),
        failed.len()
    );
    md.push_str(&details);
    if let Some(path) = out {
        std::fs::write(path, &md).with_context(|| format!("writing {path}"))?;
        println!("[explain] report written to {path}");
    }
    pool.close().await;
    if strict {
        anyhow::ensure!(
            failed.is_empty(),
            "Seq Scan detected in: {}",
            failed.iter().map(|r| r.name.as_str()).collect::<Vec<_>>().join("; ")
        );
        println!("[explain] PASS: {} queries, no Seq Scan on big tables", results.len());
    }
    Ok(results)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn detects_seq_scans() {
        let plan = json!([{"Plan": {"Node Type": "Limit", "Plans": [
            {"Node Type": "Seq Scan", "Relation Name": "books_p03"},
            {"Node Type": "Seq Scan", "Relation Name": "users"},
            {"Node Type": "Index Scan", "Relation Name": "authors_p01"},
            {"Node Type": "Seq Scan", "Relation Name": "audit_log_y2024m01"}
        ]}}]);
        assert_eq!(seq_scans_on_big_tables(&plan), vec!["books_p03", "audit_log_y2024m01"]);
        assert!(seq_scans_on_big_tables(&json!([{"Plan": {"Node Type": "Result"}}])).is_empty());
    }

    #[test]
    fn raw_placeholders() {
        let qb = raw("EXPLAIN ", "SELECT $1, $2, $1 FROM t WHERE x = $3", &[Arg::I64(1), Arg::Str("a".into()), Arg::NullTs]);
        assert_eq!(qb.into_sql(), "EXPLAIN SELECT $1, $2, $3 FROM t WHERE x = $4");
    }
}
