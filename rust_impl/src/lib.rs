//! World Library backend: catalog of books and authors at 10B+ scale on PostgreSQL.
//!
//! Layers: `http` (handlers) -> `service` (business rules, transactions, audit)
//! -> `repository` (SQL). Pure helpers: `query`, `validation`, `cursor`, `isbn`, `ids`.

pub mod config;
pub mod cursor;
pub mod db;
pub mod error;
pub mod explain;
pub mod http;
pub mod ids;
pub mod isbn;
pub mod metrics;
pub mod models;
pub mod query;
pub mod repository;
pub mod seed;
pub mod service;
pub mod validation;

use std::sync::Arc;

use crate::{config::Config, db::Db, http::AppState, ids::IdGenerator, metrics::Metrics, service::Service};

/// Builds the application state from an existing pool (used by `serve` and tests).
pub fn build_state(db: Db, node_id: u16, default_limit: u32) -> anyhow::Result<AppState> {
    let svc = Service::new(db, Arc::new(IdGenerator::new(node_id)));
    Ok(AppState { svc, metrics: Arc::new(Metrics::new()?), default_limit })
}

pub fn init_logging(level: &str) {
    let filter = tracing_subscriber::EnvFilter::try_new(level).unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info"));
    let _ = tracing_subscriber::fmt()
        .json()
        .flatten_event(true)
        .with_current_span(false)
        .with_span_list(false)
        .with_env_filter(filter)
        .try_init();
}

/// Runs the HTTP server until SIGINT/SIGTERM.
pub async fn serve(cfg: Config) -> anyhow::Result<()> {
    let db = Db::connect(&cfg).await?;
    // keep audit partitions ahead of time (idempotent, cheap)
    if let Err(e) = sqlx::query("SELECT ensure_audit_partitions(date_trunc('month', now())::date, 3)").execute(&db.pool).await {
        tracing::warn!(error = %e, "could not ensure audit partitions (migrations applied?)");
    }
    let state = build_state(db, cfg.node_id, cfg.default_page_size)?;
    let app = http::router(state, cfg.request_timeout);
    let listener = tokio::net::TcpListener::bind(&cfg.http_addr).await?;
    tracing::info!(addr = %cfg.http_addr, "listening");
    axum::serve(listener, app).with_graceful_shutdown(shutdown_signal()).await?;
    Ok(())
}

async fn shutdown_signal() {
    let ctrl_c = async {
        let _ = tokio::signal::ctrl_c().await;
    };
    #[cfg(unix)]
    let term = async {
        if let Ok(mut s) = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate()) {
            s.recv().await;
        }
    };
    #[cfg(not(unix))]
    let term = std::future::pending::<()>();
    tokio::select! {
        () = ctrl_c => {},
        () = term => {},
    }
    tracing::info!("shutdown signal received");
}
