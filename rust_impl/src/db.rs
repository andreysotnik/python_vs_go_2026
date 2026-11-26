//! Connection pool and transaction helpers.
//!
//! PgBouncer (transaction pooling) compatibility: no session state is relied upon.
//! `statement_timeout` is applied with `set_config(..., is_local => true)` inside every
//! transaction instead of a session-level `SET`.

use std::{str::FromStr, time::Duration};

use sqlx::{
    ConnectOptions, PgPool, Postgres, Transaction,
    postgres::{PgConnectOptions, PgPoolOptions},
};

use crate::{config::Config, error::AppResult};

/// Per-transaction settings, one round trip, PgBouncer-safe because they are LOCAL.
/// Every query is bounded by statement_timeout.
///
/// `plan_cache_mode` is deliberately left at `auto`: with 64 partitions a custom plan costs
/// ~4 ms of planning per query, while the cached generic plan is ~0.02 ms and (thanks to the
/// index design: leading equality columns, run-time partition pruning) is the same plan.
/// Measured: forcing custom plans cut throughput ~3x under load (see WORKLOG.md).
const SET_TX_SETTINGS: &str = "SELECT set_config('statement_timeout', $1, true)";
const SET_READ_TX_SETTINGS: &str = "SELECT set_config('statement_timeout', $1, true), set_config('transaction_read_only', 'on', true)";

#[derive(Clone, Debug)]
pub struct Db {
    pub pool: PgPool,
    pub statement_timeout: Duration,
}

pub fn connect_options(url: &str, statement_cache: usize) -> anyhow::Result<PgConnectOptions> {
    Ok(PgConnectOptions::from_str(url)?
        .application_name("world-library")
        .statement_cache_capacity(statement_cache)
        .log_slow_statements(tracing::log::LevelFilter::Warn, Duration::from_secs(1))
        .log_statements(tracing::log::LevelFilter::Trace))
}

impl Db {
    pub async fn connect(cfg: &Config) -> anyhow::Result<Self> {
        let pool = PgPoolOptions::new()
            .max_connections(cfg.db_max_connections)
            .min_connections(cfg.db_min_connections)
            .acquire_timeout(cfg.db_acquire_timeout)
            .idle_timeout(Some(Duration::from_secs(300)))
            .max_lifetime(Some(Duration::from_secs(1800)))
            .connect_with(connect_options(&cfg.database_url, cfg.db_statement_cache_capacity)?)
            .await?;
        Ok(Self { pool, statement_timeout: cfg.db_statement_timeout })
    }

    pub fn from_pool(pool: PgPool, statement_timeout: Duration) -> Self {
        Self { pool, statement_timeout }
    }

    /// Starts a transaction with a transaction-local statement_timeout.
    pub async fn begin(&self) -> AppResult<Transaction<'static, Postgres>> {
        let mut tx = self.pool.begin().await?;
        sqlx::query(SET_TX_SETTINGS).bind(format!("{}ms", self.statement_timeout.as_millis())).execute(&mut *tx).await?;
        Ok(tx)
    }

    /// Read-only transaction (lets PostgreSQL skip some bookkeeping and documents intent).
    pub async fn begin_read(&self) -> AppResult<Transaction<'static, Postgres>> {
        let mut tx = self.pool.begin().await?;
        sqlx::query(SET_READ_TX_SETTINGS).bind(format!("{}ms", self.statement_timeout.as_millis())).execute(&mut *tx).await?;
        Ok(tx)
    }

    pub async fn ping(&self) -> AppResult<()> {
        let mut tx = self.begin_read().await?;
        sqlx::query("SELECT 1").execute(&mut *tx).await?;
        tx.commit().await?;
        Ok(())
    }
}

/// Runs embedded migrations (sqlx migrate, files in ./migrations).
pub async fn migrate(url: &str) -> anyhow::Result<()> {
    let pool = PgPoolOptions::new().max_connections(1).connect_with(connect_options(url, 0)?).await?;
    sqlx::migrate!("./migrations").run(&pool).await?;
    // Pre-create audit partitions for the coming months (idempotent).
    sqlx::query("SELECT ensure_audit_partitions(date_trunc('month', now())::date, 3)").execute(&pool).await?;
    pool.close().await;
    Ok(())
}
