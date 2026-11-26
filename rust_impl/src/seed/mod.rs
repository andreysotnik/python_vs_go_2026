//! Deterministic mock-data generator + bulk loader (COPY, parallel, indexes rebuilt
//! after the load, per partition, in parallel).
//!
//! Every row is generated from `hash(SEED_RANDOM, table, row_index)`, so the data set
//! is identical regardless of chunking or the number of workers.

pub mod data;

use std::{
    fmt::Write as _,
    sync::{
        Arc,
        atomic::{AtomicU64, Ordering},
    },
    time::{Duration, Instant},
};

use anyhow::Context;
use chrono::{DateTime, Datelike, NaiveDate};
use rand::{Rng, SeedableRng, rngs::SmallRng};
use sqlx::{
    Executor, PgPool,
    postgres::{PgConnection, PgPoolOptions},
};

use crate::{config::SeedConfig, db::connect_options, ids, isbn};

use data::{BIO_WORDS, EDITIONS, GENRES, LANGS, Lang, PUBLISHER_HEADS, PUBLISHER_TAILS};

const SALT_AUTHOR: u64 = 0xA17A_0000_0000_0001;
const SALT_BOOK: u64 = 0xB00C_0000_0000_0002;
const SALT_USER: u64 = 0x05E2_0000_0000_0003;
/// Seeded data is spread over [2021-01-01, 2026-11-01) (capped at now - 1 day).
const SEED_START_MS: i64 = 1_609_459_200_000;
const SEED_END_MS: i64 = 1_793_491_200_000;
const SYSTEM_USER: i64 = 1;
const TARGET_TABLES: &[&str] = &["books", "authors", "book_authors", "author_books", "isbn_registry", "audit_log"];

// ---------------------------------------------------------------------------
// deterministic helpers
// ---------------------------------------------------------------------------

fn splitmix(mut x: u64) -> u64 {
    x = x.wrapping_add(0x9E37_79B9_7F4A_7C15);
    x = (x ^ (x >> 30)).wrapping_mul(0xBF58_476D_1CE4_E5B9);
    x = (x ^ (x >> 27)).wrapping_mul(0x94D0_49BB_1331_11EB);
    x ^ (x >> 31)
}

fn row_rng(seed: u64, salt: u64, i: u64) -> SmallRng {
    SmallRng::seed_from_u64(splitmix(seed ^ salt ^ splitmix(i)))
}

/// Static description of the data set (ids/timestamps are pure functions of the index).
#[derive(Debug, Clone, Copy)]
pub struct Plan {
    pub books: u64,
    pub authors: u64,
    pub users: u64,
    pub seed: u64,
    pub end_ms: i64,
}

impl Plan {
    fn ts_ms(&self, i: u64, n: u64) -> i64 {
        let span = (self.end_ms - SEED_START_MS).max(1) as i128;
        SEED_START_MS + (i128::from(i) * span / i128::from(n.max(1))) as i64
    }

    /// Low 22 bits of seeded ids: bit 21 separates authors from books, the rest is the
    /// index, so ids are unique as long as < 2M rows share one millisecond.
    fn seeded_id(ms: i64, kind: i64, i: u64) -> i64 {
        let low = (kind << 21) | (i as i64 & 0x1F_FFFF);
        ids::compose(ms, low >> 12, low & 0xFFF)
    }

    pub fn author_ms(&self, i: u64) -> i64 {
        self.ts_ms(i, self.authors)
    }

    pub fn author_id(&self, i: u64) -> i64 {
        Self::seeded_id(self.author_ms(i), 1, i)
    }

    pub fn book_ms(&self, i: u64) -> i64 {
        self.ts_ms(i, self.books)
    }

    pub fn book_id(&self, i: u64) -> i64 {
        Self::seeded_id(self.book_ms(i), 0, i)
    }

    /// Seeded users get ids from the very beginning of the id epoch (never clash with runtime ids).
    pub fn user_id(&self, i: u64) -> i64 {
        ids::compose(ids::EPOCH_MS + 1000 + i as i64, 1023, 0)
    }

    fn pick_user(&self, rng: &mut SmallRng) -> i64 {
        // a few "power users" create most records
        let u: f64 = rng.r#gen();
        self.user_id(((self.users as f64) * u * u) as u64)
    }

    pub fn author_lang(&self, i: u64) -> &'static Lang {
        let mut rng = row_rng(self.seed, SALT_AUTHOR, i);
        pick_lang(&mut rng)
    }
}

fn pick_lang(rng: &mut SmallRng) -> &'static Lang {
    let total: u32 = LANGS.iter().map(|l| l.weight).sum();
    let mut x = rng.gen_range(0..total);
    for l in LANGS {
        if x < l.weight {
            return l;
        }
        x -= l.weight;
    }
    &LANGS[0]
}

fn pick<'a, T>(rng: &mut SmallRng, xs: &'a [T]) -> &'a T {
    &xs[rng.gen_range(0..xs.len())]
}

fn make_title(rng: &mut SmallRng, l: &Lang) -> String {
    let t = pick(rng, l.templates);
    let mut s = t.replace("{A}", pick(rng, l.adjectives)).replace("{N}", pick(rng, l.nouns)).replace("{M}", pick(rng, l.nouns));
    if rng.gen_bool(0.08) {
        let _ = write!(s, " {}", rng.gen_range(2..=7));
    }
    s
}

// ---------------------------------------------------------------------------
// COPY text format encoding
// ---------------------------------------------------------------------------

fn put_text(buf: &mut String, s: &str) {
    for c in s.chars() {
        match c {
            '\\' => buf.push_str("\\\\"),
            '\t' => buf.push_str("\\t"),
            '\n' => buf.push_str("\\n"),
            '\r' => buf.push_str("\\r"),
            _ => buf.push(c),
        }
    }
}

fn put_opt(buf: &mut String, s: Option<&str>) {
    match s {
        Some(s) => put_text(buf, s),
        None => buf.push_str("\\N"),
    }
}

/// PostgreSQL array literal for text[] (elements quoted, `"` and `\` escaped).
fn pg_text_array(items: &[&str]) -> String {
    let mut s = String::from("{");
    for (i, it) in items.iter().enumerate() {
        if i > 0 {
            s.push(',');
        }
        s.push('"');
        for c in it.chars() {
            if c == '"' || c == '\\' {
                s.push('\\');
            }
            s.push(c);
        }
        s.push('"');
    }
    s.push('}');
    s
}

fn ts(ms: i64) -> String {
    DateTime::from_timestamp_millis(ms).unwrap_or_default().format("%Y-%m-%d %H:%M:%S%.3f+00").to_string()
}

fn sep(buf: &mut String) {
    buf.push('\t');
}

// ---------------------------------------------------------------------------
// generators
// ---------------------------------------------------------------------------

pub fn gen_users(plan: &Plan) -> String {
    let mut buf = String::new();
    for i in 0..plan.users {
        let mut rng = row_rng(plan.seed, SALT_USER, i);
        let l = pick_lang(&mut rng);
        let first = pick(&mut rng, l.first).1;
        let name: String = first.chars().filter(char::is_ascii_alphanumeric).collect::<String>().to_lowercase();
        let name = if name.len() < 3 { format!("user{name}") } else { name };
        let _ = writeln!(buf, "{}\t{}_{}\t{}.{}@example.org\t{}", plan.user_id(i), name, i, name, i, ts(ids::EPOCH_MS + 1000 + i as i64));
    }
    buf
}

/// Authors rows `[lo, hi)` + their `create` audit entries.
pub fn gen_authors(plan: &Plan, lo: u64, hi: u64) -> (String, String) {
    let mut out = String::with_capacity(((hi - lo) * 200) as usize);
    let mut audit = String::with_capacity(((hi - lo) * 100) as usize);
    for i in lo..hi {
        let mut rng = row_rng(plan.seed, SALT_AUTHOR, i);
        let l = pick_lang(&mut rng); // must stay the first draw (see Plan::author_lang)
        let (fnat, flat) = *pick(&mut rng, l.first);
        let (lnat, llat) = *pick(&mut rng, l.last);
        let east = matches!(l.code, "zh" | "ja");
        let (native, latin) =
            if east { (format!("{lnat}{fnat}"), format!("{llat} {flat}")) } else { (format!("{fnat} {lnat}"), format!("{flat} {llat}")) };
        // disambiguate homonyms like real catalogs do ("Smith, John (1950-)")
        let latin = if rng.gen_bool(0.3) { format!("{latin} {}.", char::from(b'A' + rng.gen_range(0..26u8))) } else { latin };
        let original = (native != latin).then_some(native);
        let birth = rng.gen_bool(0.85).then(|| {
            let u: f64 = rng.r#gen();
            let year = 2005 - (u * u * 480.0) as i32;
            NaiveDate::from_ymd_opt(year, rng.gen_range(1..=12), rng.gen_range(1..=28)).unwrap_or_default()
        });
        let death = birth.and_then(|b| {
            let d = b.year() + rng.gen_range(35..=95);
            (b.year() < 1950 && d <= 2025).then(|| NaiveDate::from_ymd_opt(d, rng.gen_range(1..=12), rng.gen_range(1..=28)).unwrap_or(b))
        });
        let country = *pick(&mut rng, l.countries);
        let bio = rng.gen_bool(0.3).then(|| {
            let n = rng.gen_range(6..16);
            (0..n).map(|_| *pick(&mut rng, BIO_WORDS)).collect::<Vec<_>>().join(" ")
        });
        let ext = if rng.gen_bool(0.4) {
            format!(r#"{{"viaf": "{}", "wikidata": "Q{}"}}"#, 10_000_000 + i, 100_000 + i * 7)
        } else {
            "{}".to_string()
        };
        let ms = plan.author_ms(i);
        let user = plan.pick_user(&mut rng);
        let id = plan.author_id(i);
        let _ = write!(out, "{id}");
        sep(&mut out);
        put_text(&mut out, &latin);
        sep(&mut out);
        put_opt(&mut out, original.as_deref());
        sep(&mut out);
        put_opt(&mut out, birth.map(|d| d.format("%Y-%m-%d").to_string()).as_deref());
        sep(&mut out);
        put_opt(&mut out, death.map(|d| d.format("%Y-%m-%d").to_string()).as_deref());
        sep(&mut out);
        put_text(&mut out, country);
        sep(&mut out);
        put_opt(&mut out, bio.as_deref());
        sep(&mut out);
        put_text(&mut out, &ext);
        let t = ts(ms);
        let _ = writeln!(out, "\t{t}\t{user}\t{t}\t{user}");
        let _ = writeln!(audit, "{id}\tauthor\t{id}\tcreate\t{user}\t{t}\t{{\"source\": \"seed\"}}");
    }
    (out, audit)
}

/// COPY payloads produced for a chunk of books.
#[derive(Default)]
pub struct BookChunk {
    pub books: String,
    pub isbn: String,
    pub book_authors: String,
    pub author_books: String,
    pub audit: String,
    pub links: u64,
}

pub fn gen_books(plan: &Plan, lo: u64, hi: u64) -> BookChunk {
    let n = (hi - lo) as usize;
    let mut c = BookChunk {
        books: String::with_capacity(n * 400),
        isbn: String::with_capacity(n * 40),
        book_authors: String::with_capacity(n * 60),
        author_books: String::with_capacity(n * 60),
        audit: String::with_capacity(n * 100),
        links: 0,
    };
    let roles = ["co-author", "co-author", "co-author", "translator", "translator", "editor", "illustrator"];
    for i in lo..hi {
        let mut rng = row_rng(plan.seed, SALT_BOOK, i);
        let id = plan.book_id(i);
        // long tail: author index ~ A * u^3 -> a small head of authors owns most books
        let n_auth = match rng.gen_range(0..100) {
            0..=79 => 1,
            80..=94 => 2,
            _ => rng.gen_range(3..=4),
        };
        let mut links: Vec<(u64, &str)> = Vec::with_capacity(n_auth);
        for k in 0..n_auth {
            let u: f64 = rng.r#gen();
            let a = (((plan.authors as f64) * u * u * u) as u64).min(plan.authors - 1);
            let role = if k == 0 { "author" } else { *pick(&mut rng, &roles) };
            if !links.iter().any(|(x, r)| *x == a && *r == role) {
                links.push((a, role));
            }
        }
        let author_lang = plan.author_lang(links[0].0);
        let lang = if rng.gen_bool(0.75) { author_lang } else { pick_lang(&mut rng) };
        let title = make_title(&mut rng, lang);
        let original_title = (lang.code != author_lang.code).then(|| make_title(&mut rng, author_lang));
        let subtitle = rng.gen_bool(0.2).then(|| pick(&mut rng, lang.subtitles).to_string());
        let description = rng.gen_bool(0.3).then(|| {
            let k = rng.gen_range(8..24);
            (0..k)
                .map(|_| if rng.gen_bool(0.5) { *pick(&mut rng, lang.nouns) } else { *pick(&mut rng, lang.adjectives) })
                .collect::<Vec<_>>()
                .join(" ")
        });
        let (isbn13, isbn10) = if rng.gen_bool(0.92) {
            let prefix = if rng.gen_bool(0.1) { 979 } else { 978 };
            // bijection on [0, 1e9): multiplying by 3^18 (coprime with 10^9) keeps ISBNs unique
            let body = (i.wrapping_mul(387_420_489) + plan.seed) % 1_000_000_000;
            let s13 = isbn::make_isbn13(prefix, body);
            let s10 = isbn::isbn13_to_10(&s13);
            (Some(s13), s10)
        } else {
            (None, None)
        };
        let year = if rng.gen_bool(0.95) {
            let u: f64 = rng.r#gen();
            Some(2025 - (u * u * u * 600.0) as i32)
        } else {
            None
        };
        let publisher = rng.gen_bool(0.9).then(|| {
            let u: f64 = rng.r#gen();
            let k = ((PUBLISHER_HEADS.len() * PUBLISHER_TAILS.len()) as f64 * u * u) as usize;
            format!("{} {}", PUBLISHER_HEADS[k % PUBLISHER_HEADS.len()], PUBLISHER_TAILS[k / PUBLISHER_HEADS.len() % PUBLISHER_TAILS.len()])
        });
        let pages = rng.gen_bool(0.93).then(|| {
            let u: f64 = rng.r#gen();
            40 + (u * u * 1200.0) as i32
        });
        let ng = rng.gen_range(1..=3);
        let mut genres: Vec<&str> = Vec::with_capacity(ng);
        for _ in 0..ng {
            let g = *pick(&mut rng, GENRES);
            if !genres.contains(&g) {
                genres.push(g);
            }
        }
        let edition = rng.gen_bool(0.1).then(|| *pick(&mut rng, EDITIONS));
        let ext = if rng.gen_bool(0.5) { format!(r#"{{"openlibrary": "OL{}M"}}"#, 1_000_000 + i) } else { "{}".to_string() };
        let user = plan.pick_user(&mut rng);
        let t = ts(plan.book_ms(i));

        let b = &mut c.books;
        let _ = write!(b, "{id}");
        sep(b);
        put_text(b, &title);
        sep(b);
        put_opt(b, original_title.as_deref());
        sep(b);
        put_opt(b, subtitle.as_deref());
        sep(b);
        put_opt(b, description.as_deref());
        sep(b);
        put_opt(b, isbn10.as_deref());
        sep(b);
        put_opt(b, isbn13.as_deref());
        sep(b);
        put_text(b, lang.code);
        sep(b);
        put_opt(b, year.map(|y| y.to_string()).as_deref());
        sep(b);
        put_opt(b, publisher.as_deref());
        sep(b);
        put_opt(b, pages.map(|p| p.to_string()).as_deref());
        sep(b);
        put_text(b, &pg_text_array(&genres));
        sep(b);
        put_opt(b, edition);
        sep(b);
        put_text(b, &ext);
        let _ = writeln!(b, "\t{t}\t{user}\t{t}\t{user}");

        if let Some(s) = &isbn13 {
            let _ = writeln!(c.isbn, "{s}\t{id}");
        }
        for (k, (a, role)) in links.iter().enumerate() {
            let aid = plan.author_id(*a);
            let _ = writeln!(c.book_authors, "{id}\t{aid}\t{role}\t{}", k + 1);
            let _ = writeln!(c.author_books, "{aid}\t{id}\t{role}\t{}", k + 1);
            c.links += 1;
        }
        let _ = writeln!(c.audit, "{id}\tbook\t{id}\tcreate\t{user}\t{t}\t{{\"source\": \"seed\"}}");
    }
    c
}

// ---------------------------------------------------------------------------
// loader
// ---------------------------------------------------------------------------

async fn copy(conn: &mut PgConnection, stmt: &str, data: &str) -> anyhow::Result<()> {
    if data.is_empty() {
        return Ok(());
    }
    let mut cp = conn.copy_in_raw(stmt).await.with_context(|| stmt.to_string())?;
    for part in data.as_bytes().chunks(4 << 20) {
        cp.send(part).await?;
    }
    cp.finish().await.with_context(|| stmt.to_string())?;
    Ok(())
}

const COPY_AUTHORS: &str = "COPY authors (id, full_name, original_name, birth_date, death_date, country_code, biography, \
    external_ids, created_at, created_by, updated_at, updated_by) FROM STDIN";
const COPY_BOOKS: &str = "COPY books (id, title, original_title, subtitle, description, isbn_10, isbn_13, language_code, \
    publication_year, publisher, page_count, genres, edition, external_ids, created_at, created_by, updated_at, updated_by) FROM STDIN";
const COPY_AUDIT: &str = "COPY audit_log (id, entity_type, entity_id, action, changed_by, changed_at, diff) FROM STDIN";

struct Progress {
    label: &'static str,
    total: u64,
    done: AtomicU64,
}

fn spawn_reporter(p: Arc<Progress>, start: Instant) -> tokio::task::JoinHandle<()> {
    tokio::spawn(async move {
        loop {
            tokio::time::sleep(Duration::from_secs(2)).await;
            let d = p.done.load(Ordering::Relaxed);
            let secs = start.elapsed().as_secs_f64().max(0.001);
            println!(
                "[seed] {:<8} {:>11}/{} ({:5.1}%)  {:>9.0} rows/s",
                p.label,
                d,
                p.total,
                d as f64 * 100.0 / p.total.max(1) as f64,
                d as f64 / secs
            );
            if d >= p.total {
                break;
            }
        }
    })
}

/// Saved DDL of secondary indexes / FKs that are dropped for the bulk load.
async fn capture_and_drop_ddl(pool: &PgPool) -> anyhow::Result<()> {
    pool.execute("CREATE TABLE IF NOT EXISTS seed_pending_ddl (ord serial PRIMARY KEY, kind text NOT NULL, tbl text NOT NULL, name text NOT NULL, def text NOT NULL)").await?;
    let (pending,): (i64,) = sqlx::query_as("SELECT count(*) FROM seed_pending_ddl").fetch_one(pool).await?;
    if pending > 0 {
        println!("[seed] found DDL saved by an interrupted run; it will be restored after loading");
        return Ok(());
    }
    let tables: Vec<String> = TARGET_TABLES.iter().map(|s| (*s).to_string()).collect();
    sqlx::query(
        "INSERT INTO seed_pending_ddl (kind, tbl, name, def) \
         SELECT 'index', t.relname, ic.relname, pg_get_indexdef(i.indexrelid) \
         FROM pg_index i JOIN pg_class t ON t.oid = i.indrelid JOIN pg_class ic ON ic.oid = i.indexrelid \
         JOIN pg_namespace n ON n.oid = t.relnamespace \
         WHERE n.nspname = current_schema() AND t.relname = ANY($1) AND NOT i.indisprimary AND NOT i.indisunique \
         ORDER BY t.relname, ic.relname",
    )
    .bind(&tables)
    .execute(pool)
    .await?;
    sqlx::query(
        "INSERT INTO seed_pending_ddl (kind, tbl, name, def) \
         SELECT 'fk', t.relname, c.conname, pg_get_constraintdef(c.oid) \
         FROM pg_constraint c JOIN pg_class t ON t.oid = c.conrelid JOIN pg_namespace n ON n.oid = t.relnamespace \
         WHERE c.contype = 'f' AND c.conparentid = 0 AND n.nspname = current_schema() AND t.relname = ANY($1) \
         ORDER BY t.relname, c.conname",
    )
    .bind(&tables)
    .execute(pool)
    .await?;
    let rows: Vec<(String, String, String)> =
        sqlx::query_as("SELECT kind, tbl, name FROM seed_pending_ddl ORDER BY ord").fetch_all(pool).await?;
    for (kind, tbl, name) in rows {
        let stmt = if kind == "fk" {
            format!("ALTER TABLE {tbl} DROP CONSTRAINT IF EXISTS {name}")
        } else {
            format!("DROP INDEX IF EXISTS {name}")
        };
        pool.execute(stmt.as_str()).await?;
    }
    Ok(())
}

/// Rebuilds dropped indexes partition by partition in parallel, then re-adds FKs.
async fn restore_ddl(pool: &PgPool, workers: usize) -> anyhow::Result<()> {
    let rows: Vec<(String, String, String, String)> =
        sqlx::query_as("SELECT kind, tbl, name, def FROM seed_pending_ddl ORDER BY ord").fetch_all(pool).await?;
    let mut jobs: Vec<(String, String)> = Vec::new(); // (child index stmt, attach stmt)
    for (kind, tbl, name, def) in rows.iter().filter(|r| r.0 == "index").map(|r| (&r.0, &r.1, &r.2, &r.3)) {
        let _ = kind;
        let rest = def.find(" USING ").map(|p| &def[p..]).with_context(|| format!("unexpected index def: {def}"))?;
        pool.execute(format!("CREATE INDEX IF NOT EXISTS {name} ON ONLY {tbl}{rest}").as_str()).await?;
        let parts: Vec<(String,)> = sqlx::query_as(
            "SELECT c.relname::text FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid \
             WHERE i.inhparent = $1::regclass ORDER BY 1",
        )
        .bind(tbl)
        .fetch_all(pool)
        .await?;
        for (child,) in parts {
            let suffix = child.rsplit('_').next().unwrap_or("p");
            let child_idx = format!("{name}_{suffix}");
            jobs.push((
                format!("CREATE INDEX IF NOT EXISTS {child_idx} ON {child}{rest}"),
                format!("ALTER INDEX {name} ATTACH PARTITION {child_idx}"),
            ));
        }
    }
    let total = jobs.len() as u64;
    let jobs = Arc::new(std::sync::Mutex::new(jobs));
    let done = Arc::new(AtomicU64::new(0));
    let mut handles = Vec::new();
    for _ in 0..workers {
        let (pool, jobs, done) = (pool.clone(), jobs.clone(), done.clone());
        handles.push(tokio::spawn(async move {
            loop {
                let job = jobs.lock().map_err(|_| anyhow::anyhow!("poisoned"))?.pop();
                let Some((create, attach)) = job else { break };
                let mut conn = pool.acquire().await?;
                conn.execute(create.as_str()).await.with_context(|| create.clone())?;
                conn.execute(attach.as_str()).await.with_context(|| attach.clone())?;
                let d = done.fetch_add(1, Ordering::Relaxed) + 1;
                if d % 64 == 0 || d == total {
                    println!("[seed] indexes  {d}/{total} partition indexes built");
                }
            }
            anyhow::Ok(())
        }));
    }
    for h in handles {
        h.await??;
    }
    for (_, tbl, name, def) in rows.iter().filter(|r| r.0 == "fk") {
        let t = Instant::now();
        pool.execute(format!("ALTER TABLE {tbl} ADD CONSTRAINT {name} {def}").as_str()).await?;
        println!("[seed] fk       {name} validated in {:.1}s", t.elapsed().as_secs_f64());
    }
    pool.execute("DROP TABLE seed_pending_ddl").await?;
    Ok(())
}

pub async fn run(cfg: SeedConfig) -> anyhow::Result<()> {
    let t0 = Instant::now();
    let opts = connect_options(&cfg.database_url, 64)?.options([
        ("statement_timeout", "0"),
        ("maintenance_work_mem", "512MB"),
        ("synchronous_commit", "off"),
        ("max_parallel_maintenance_workers", "2"),
    ]);
    let pool = PgPoolOptions::new()
        .max_connections(u32::try_from(cfg.workers).unwrap_or(8) + 2)
        .acquire_timeout(Duration::from_secs(60))
        .connect_with(opts)
        .await?;
    // fixed end of the time range => identical ids/timestamps for the same SEED_RANDOM
    let end_ms = SEED_END_MS.min(chrono::Utc::now().timestamp_millis() - 86_400_000);
    let plan = Plan { books: cfg.books, authors: cfg.authors, users: cfg.users, seed: cfg.random_seed, end_ms };
    println!(
        "[seed] books={} authors={} users={} seed={} workers={} batch={}",
        cfg.books, cfg.authors, cfg.users, cfg.random_seed, cfg.workers, cfg.batch_size
    );
    sqlx::query("SELECT ensure_audit_partitions('2020-01-01'::date, 1)").execute(&pool).await?;
    if cfg.truncate {
        pool.execute("TRUNCATE book_authors, author_books, isbn_registry, books, authors, audit_log").await?;
        sqlx::query("DELETE FROM users WHERE id <> $1").bind(SYSTEM_USER).execute(&pool).await?;
    }
    capture_and_drop_ddl(&pool).await?;
    let t_drop = t0.elapsed();

    // users (small)
    {
        let mut conn = pool.acquire().await?;
        copy(&mut conn, "COPY users (id, username, email, created_at) FROM STDIN", &gen_users(&plan)).await?;
    }

    let total_rows = Arc::new(AtomicU64::new(cfg.users));
    let load_start = Instant::now();

    // authors
    let t_auth = Instant::now();
    run_chunks(&pool, &cfg, "authors", cfg.authors, total_rows.clone(), move |lo, hi| {
        let (a, audit) = gen_authors(&plan, lo, hi);
        vec![(COPY_AUTHORS, a), (COPY_AUDIT, audit)]
    })
    .await?;
    let auth_secs = t_auth.elapsed().as_secs_f64();

    // books + links + isbn registry + audit
    let t_books = Instant::now();
    run_chunks(&pool, &cfg, "books", cfg.books, total_rows.clone(), move |lo, hi| {
        let c = gen_books(&plan, lo, hi);
        vec![
            (COPY_BOOKS, c.books),
            ("COPY isbn_registry (isbn_13, book_id) FROM STDIN", c.isbn),
            ("COPY book_authors (book_id, author_id, role, ordinal) FROM STDIN", c.book_authors),
            ("COPY author_books (author_id, book_id, role, ordinal) FROM STDIN", c.author_books),
            (COPY_AUDIT, c.audit),
        ]
    })
    .await?;
    let books_secs = t_books.elapsed().as_secs_f64();
    let load_secs = load_start.elapsed().as_secs_f64();
    let rows = total_rows.load(Ordering::Relaxed);

    let t_idx = Instant::now();
    restore_ddl(&pool, cfg.workers).await?;
    let idx_secs = t_idx.elapsed().as_secs_f64();

    let t_an = Instant::now();
    for t in ["users", "authors", "books", "book_authors", "author_books", "isbn_registry", "audit_log"] {
        pool.execute(format!("VACUUM (ANALYZE) {t}").as_str()).await?;
    }
    let an_secs = t_an.elapsed().as_secs_f64();
    let total = t0.elapsed().as_secs_f64();
    println!("[seed] ---------------------------------------------------------------");
    println!("[seed] authors loaded in {auth_secs:.1}s ({:.0} authors/s)", cfg.authors as f64 / auth_secs.max(0.001));
    println!("[seed] books   loaded in {books_secs:.1}s ({:.0} books/s)", cfg.books as f64 / books_secs.max(0.001));
    println!("[seed] COPY phase: {rows} rows (all tables) in {load_secs:.1}s = {:.0} rows/s", rows as f64 / load_secs.max(0.001));
    println!("[seed] drop ddl {:.1}s, index+fk rebuild {idx_secs:.1}s, vacuum analyze {an_secs:.1}s", t_drop.as_secs_f64());
    println!("[seed] TOTAL {total:.1}s; end-to-end {:.0} rows/s", rows as f64 / total.max(0.001));
    pool.close().await;
    Ok(())
}

type ChunkFn = dyn Fn(u64, u64) -> Vec<(&'static str, String)> + Send + Sync;

async fn run_chunks<F>(pool: &PgPool, cfg: &SeedConfig, label: &'static str, n: u64, total_rows: Arc<AtomicU64>, f: F) -> anyhow::Result<()>
where
    F: Fn(u64, u64) -> Vec<(&'static str, String)> + Send + Sync + 'static,
{
    if n == 0 {
        return Ok(());
    }
    let f: Arc<ChunkFn> = Arc::new(f);
    let batch = cfg.batch_size as u64;
    let chunks = n.div_ceil(batch);
    let next = Arc::new(AtomicU64::new(0));
    let progress = Arc::new(Progress { label, total: n, done: AtomicU64::new(0) });
    let reporter = spawn_reporter(progress.clone(), Instant::now());
    let mut handles = Vec::new();
    for _ in 0..cfg.workers.min(chunks as usize).max(1) {
        let (pool, f, next, progress, total_rows) = (pool.clone(), f.clone(), next.clone(), progress.clone(), total_rows.clone());
        handles.push(tokio::spawn(async move {
            let mut conn = pool.acquire().await?;
            loop {
                let k = next.fetch_add(1, Ordering::Relaxed);
                if k >= chunks {
                    break;
                }
                let (lo, hi) = (k * batch, ((k + 1) * batch).min(n));
                let f2 = f.clone();
                let payloads = tokio::task::spawn_blocking(move || f2(lo, hi)).await?;
                for (stmt, data) in &payloads {
                    copy(&mut conn, stmt, data).await?;
                    total_rows.fetch_add(data.bytes().filter(|b| *b == b'\n').count() as u64, Ordering::Relaxed);
                }
                progress.done.fetch_add(hi - lo, Ordering::Relaxed);
            }
            anyhow::Ok(())
        }));
    }
    for h in handles {
        h.await??;
    }
    progress.done.store(n, Ordering::Relaxed);
    let _ = reporter.await;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn plan() -> Plan {
        Plan { books: 2000, authors: 300, users: 10, seed: 7, end_ms: 1_750_000_000_000 }
    }

    #[test]
    fn deterministic_and_chunk_independent() {
        let p = plan();
        let whole = gen_books(&p, 0, 200);
        let a = gen_books(&p, 0, 100);
        let b = gen_books(&p, 100, 200);
        assert_eq!(whole.books, format!("{}{}", a.books, b.books));
        assert_eq!(whole.book_authors, format!("{}{}", a.book_authors, b.book_authors));
        let (x, _) = gen_authors(&p, 0, 50);
        let (y, _) = gen_authors(&p, 0, 50);
        assert_eq!(x, y);
    }

    #[test]
    fn ids_unique_and_isbns_valid() {
        let p = plan();
        let mut ids: Vec<i64> = (0..p.books).map(|i| p.book_id(i)).chain((0..p.authors).map(|i| p.author_id(i))).collect();
        ids.sort_unstable();
        let n = ids.len();
        ids.dedup();
        assert_eq!(ids.len(), n);
        let c = gen_books(&p, 0, 2000);
        let mut isbns: Vec<&str> = c.isbn.lines().map(|l| l.split('\t').next().unwrap_or_default()).collect();
        assert!(isbns.len() > 1700);
        assert!(isbns.iter().all(|s| isbn::is_valid_isbn13(s)));
        isbns.sort_unstable();
        let m = isbns.len();
        isbns.dedup();
        assert_eq!(isbns.len(), m);
        assert_eq!(c.books.lines().count(), 2000);
        assert!(c.books.lines().all(|l| l.split('\t').count() == 18));
    }

    #[test]
    fn long_tail_distribution() {
        let p = Plan { books: 20_000, authors: 1000, ..plan() };
        let c = gen_books(&p, 0, 20_000);
        let mut counts = std::collections::HashMap::<&str, usize>::new();
        for l in c.author_books.lines() {
            *counts.entry(l.split('\t').next().unwrap_or_default()).or_default() += 1;
        }
        let mut v: Vec<usize> = counts.values().copied().collect();
        v.sort_unstable_by(|a, b| b.cmp(a));
        let top: usize = v.iter().take(10).sum(); // top 1% of authors
        assert!(top * 100 / c.links as usize >= 15, "top 1% authors own {top} of {} links", c.links);
    }

    #[test]
    fn multilingual_content() {
        let p = plan();
        let (a, audit) = gen_authors(&p, 0, 300);
        assert_eq!(audit.lines().count(), 300);
        let has = |pred: fn(char) -> bool| a.chars().any(pred);
        assert!(has(|c| ('\u{0400}'..='\u{04FF}').contains(&c)), "cyrillic");
        assert!(has(|c| ('\u{4E00}'..='\u{9FFF}').contains(&c)), "cjk");
        assert!(has(|c| ('\u{0600}'..='\u{06FF}').contains(&c)), "arabic");
        assert!(a.lines().all(|l| l.split('\t').count() == 12));
        let u = gen_users(&p);
        assert_eq!(u.lines().count(), 10);
    }

    #[test]
    fn copy_escaping() {
        let mut b = String::new();
        put_text(&mut b, "a\tb\nc\\d\re");
        assert_eq!(b, "a\\tb\\nc\\\\d\\re");
        let mut b = String::new();
        put_opt(&mut b, None);
        assert_eq!(b, "\\N");
        assert_eq!(pg_text_array(&["a", "b\"c", "d\\e"]), r#"{"a","b\"c","d\\e"}"#);
        assert_eq!(ts(0), "1970-01-01 00:00:00.000+00");
    }
}
