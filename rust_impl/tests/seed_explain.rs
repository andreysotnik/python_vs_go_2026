//! End-to-end check of the bulk loader and the plan checker on a small, exclusive database.

mod common;

use sqlx::Connection;
use world_library::{config::SeedConfig, explain, seed};

#[tokio::test]
async fn seed_then_explain() {
    let url = common::fresh_database("seed").await;
    let cfg = SeedConfig {
        database_url: url.clone(),
        books: 3000,
        authors: 400,
        users: 25,
        random_seed: 7,
        batch_size: 700,
        workers: 3,
        truncate: true,
    };
    seed::run(cfg.clone()).await.expect("seed");

    let mut conn = sqlx::PgConnection::connect(&url).await.unwrap();
    let count = |sql: &'static str| sqlx::query_scalar::<_, i64>(sql);
    assert_eq!(count("SELECT count(*) FROM books").fetch_one(&mut conn).await.unwrap(), 3000);
    assert_eq!(count("SELECT count(*) FROM authors").fetch_one(&mut conn).await.unwrap(), 400);
    assert_eq!(count("SELECT count(*) FROM users").fetch_one(&mut conn).await.unwrap(), 26); // + system
    let links = count("SELECT count(*) FROM book_authors").fetch_one(&mut conn).await.unwrap();
    assert_eq!(links, count("SELECT count(*) FROM author_books").fetch_one(&mut conn).await.unwrap());
    assert!(links >= 3000);
    assert_eq!(
        count("SELECT count(*) FROM isbn_registry r JOIN books b ON b.id = r.book_id AND b.isbn_13 = r.isbn_13")
            .fetch_one(&mut conn)
            .await
            .unwrap(),
        count("SELECT count(*) FROM books WHERE isbn_13 IS NOT NULL").fetch_one(&mut conn).await.unwrap()
    );
    assert_eq!(count("SELECT count(*) FROM audit_log").fetch_one(&mut conn).await.unwrap(), 3400);
    // dropped indexes and FKs were restored, bookkeeping table removed
    let idx = count("SELECT count(*) FROM pg_indexes WHERE tablename = 'books' AND indexname = 'books_fts_idx'")
        .fetch_one(&mut conn)
        .await
        .unwrap();
    assert_eq!(idx, 1);
    let fks = count("SELECT count(*) FROM pg_constraint WHERE contype = 'f' AND conname LIKE 'book_authors_%'")
        .fetch_one(&mut conn)
        .await
        .unwrap();
    assert!(fks >= 2);
    let pending = count("SELECT count(*) FROM pg_tables WHERE tablename = 'seed_pending_ddl'").fetch_one(&mut conn).await.unwrap();
    assert_eq!(pending, 0);
    // multilingual data with valid ISBNs
    let cyr = count("SELECT count(*) FROM authors WHERE original_name ~ '[А-Яа-я]'").fetch_one(&mut conn).await.unwrap();
    let cjk = count("SELECT count(*) FROM books WHERE title ~ '[一-龥]'").fetch_one(&mut conn).await.unwrap();
    assert!(cyr > 0 && cjk > 0);
    let isbns: Vec<String> =
        sqlx::query_scalar("SELECT isbn_13 FROM books WHERE isbn_13 IS NOT NULL LIMIT 200").fetch_all(&mut conn).await.unwrap();
    assert!(isbns.iter().all(|i| world_library::isbn::is_valid_isbn13(i)));

    // deterministic: re-seeding with the same SEED_RANDOM yields identical data
    let digest = "SELECT md5(string_agg(id::text || title || coalesce(isbn_13, ''), ',' ORDER BY id)) FROM books";
    let before: String = sqlx::query_scalar(digest).fetch_one(&mut conn).await.unwrap();
    drop(conn);
    seed::run(cfg).await.expect("re-seed");
    let mut conn = sqlx::PgConnection::connect(&url).await.unwrap();
    let after: String = sqlx::query_scalar(digest).fetch_one(&mut conn).await.unwrap();
    assert_eq!(before, after);

    // the plan checker runs end-to-end (non-strict: a 3k-row table may legitimately be seq-scanned)
    let out = std::env::temp_dir().join(format!("plans_{}.md", std::process::id()));
    let results = explain::run_with(&url, out.to_str(), false).await.expect("explain");
    assert!(results.len() >= 25);
    let md = std::fs::read_to_string(&out).unwrap();
    assert!(md.contains("## Summary") && md.contains("EXPLAIN") || md.contains("Execution"));
    let _ = std::fs::remove_file(out);
}
