//! 12-factor configuration: everything comes from environment variables.

use std::{collections::HashMap, str::FromStr, time::Duration};

use anyhow::{Context, anyhow};

/// Runtime configuration of the HTTP server and the database pool.
#[derive(Debug, Clone)]
pub struct Config {
    pub database_url: String,
    pub http_addr: String,
    pub db_max_connections: u32,
    pub db_min_connections: u32,
    pub db_acquire_timeout: Duration,
    pub db_statement_timeout: Duration,
    pub db_statement_cache_capacity: usize,
    pub request_timeout: Duration,
    pub node_id: u16,
    pub log_level: String,
    pub default_page_size: u32,
}

/// Configuration of the `seed` command.
#[derive(Debug, Clone)]
pub struct SeedConfig {
    pub database_url: String,
    pub books: u64,
    pub authors: u64,
    pub users: u64,
    pub random_seed: u64,
    pub batch_size: usize,
    pub workers: usize,
    pub truncate: bool,
}

pub const MAX_PAGE_SIZE: u32 = 100;

fn parse<T: FromStr>(env: &HashMap<String, String>, key: &str, default: T) -> anyhow::Result<T>
where
    T::Err: std::fmt::Display,
{
    match env.get(key) {
        None => Ok(default),
        Some(v) if v.trim().is_empty() => Ok(default),
        Some(v) => v.trim().parse::<T>().map_err(|e| anyhow!("invalid value for {key}={v:?}: {e}")),
    }
}

fn parse_bool(env: &HashMap<String, String>, key: &str, default: bool) -> anyhow::Result<bool> {
    match env.get(key).map(|v| v.trim().to_ascii_lowercase()) {
        None => Ok(default),
        Some(v) if v.is_empty() => Ok(default),
        Some(v) => match v.as_str() {
            "1" | "true" | "yes" | "on" => Ok(true),
            "0" | "false" | "no" | "off" => Ok(false),
            _ => Err(anyhow!("invalid boolean for {key}={v:?}")),
        },
    }
}

impl Config {
    pub fn from_env() -> anyhow::Result<Self> {
        Self::from_map(&std::env::vars().collect())
    }

    pub fn from_map(env: &HashMap<String, String>) -> anyhow::Result<Self> {
        let database_url = env.get("DATABASE_URL").cloned().context("DATABASE_URL is required")?;
        let cfg = Self {
            database_url,
            http_addr: env.get("HTTP_ADDR").cloned().unwrap_or_else(|| "0.0.0.0:8080".into()),
            db_max_connections: parse(env, "DB_MAX_CONNECTIONS", 32)?,
            db_min_connections: parse(env, "DB_MIN_CONNECTIONS", 2)?,
            db_acquire_timeout: Duration::from_millis(parse(env, "DB_ACQUIRE_TIMEOUT_MS", 3000)?),
            db_statement_timeout: Duration::from_millis(parse(env, "DB_STATEMENT_TIMEOUT_MS", 5000)?),
            db_statement_cache_capacity: parse(env, "DB_STATEMENT_CACHE_CAPACITY", 256)?,
            request_timeout: Duration::from_millis(parse(env, "REQUEST_TIMEOUT_MS", 15000)?),
            node_id: parse(env, "NODE_ID", 1)?,
            log_level: env.get("LOG_LEVEL").cloned().unwrap_or_else(|| "info".into()),
            default_page_size: parse(env, "DEFAULT_PAGE_SIZE", 20)?,
        };
        if cfg.node_id > crate::ids::MAX_NODE_ID {
            return Err(anyhow!("NODE_ID must be <= {}", crate::ids::MAX_NODE_ID));
        }
        if cfg.default_page_size == 0 || cfg.default_page_size > MAX_PAGE_SIZE {
            return Err(anyhow!("DEFAULT_PAGE_SIZE must be in 1..={MAX_PAGE_SIZE}"));
        }
        if cfg.db_statement_timeout.is_zero() {
            return Err(anyhow!("DB_STATEMENT_TIMEOUT_MS must be > 0"));
        }
        Ok(cfg)
    }
}

impl SeedConfig {
    pub fn from_env() -> anyhow::Result<Self> {
        Self::from_map(&std::env::vars().collect())
    }

    pub fn from_map(env: &HashMap<String, String>) -> anyhow::Result<Self> {
        let database_url = env.get("DATABASE_URL").cloned().context("DATABASE_URL is required")?;
        let cfg = Self {
            database_url,
            books: parse(env, "SEED_BOOKS", 1_000_000)?,
            authors: parse(env, "SEED_AUTHORS", 200_000)?,
            users: parse(env, "SEED_USERS", 1_000)?,
            random_seed: parse(env, "SEED_RANDOM", 42)?,
            batch_size: parse(env, "SEED_BATCH_SIZE", 50_000)?,
            workers: parse(env, "SEED_WORKERS", 8)?,
            truncate: parse_bool(env, "SEED_TRUNCATE", true)?,
        };
        if cfg.authors == 0 && cfg.books > 0 {
            return Err(anyhow!("SEED_AUTHORS must be > 0 when SEED_BOOKS > 0"));
        }
        if cfg.users == 0 || cfg.workers == 0 || cfg.batch_size == 0 {
            return Err(anyhow!("SEED_USERS, SEED_WORKERS and SEED_BATCH_SIZE must be > 0"));
        }
        Ok(cfg)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn env(pairs: &[(&str, &str)]) -> HashMap<String, String> {
        pairs.iter().map(|(k, v)| ((*k).to_string(), (*v).to_string())).collect()
    }

    #[test]
    fn defaults_apply() {
        let c = Config::from_map(&env(&[("DATABASE_URL", "postgres://x")])).unwrap();
        assert_eq!(c.http_addr, "0.0.0.0:8080");
        assert_eq!(c.db_max_connections, 32);
        assert_eq!(c.db_statement_timeout, Duration::from_millis(5000));
        assert_eq!(c.default_page_size, 20);
    }

    #[test]
    fn database_url_required() {
        assert!(Config::from_map(&env(&[])).is_err());
        assert!(SeedConfig::from_map(&env(&[])).is_err());
    }

    #[test]
    fn invalid_values_rejected() {
        assert!(Config::from_map(&env(&[("DATABASE_URL", "x"), ("DB_MAX_CONNECTIONS", "abc")])).is_err());
        assert!(Config::from_map(&env(&[("DATABASE_URL", "x"), ("DEFAULT_PAGE_SIZE", "500")])).is_err());
        assert!(Config::from_map(&env(&[("DATABASE_URL", "x"), ("NODE_ID", "5000")])).is_err());
        assert!(Config::from_map(&env(&[("DATABASE_URL", "x"), ("DB_STATEMENT_TIMEOUT_MS", "0")])).is_err());
    }

    #[test]
    fn seed_config() {
        let c = SeedConfig::from_map(&env(&[("DATABASE_URL", "x"), ("SEED_BOOKS", "10"), ("SEED_TRUNCATE", "no")])).unwrap();
        assert_eq!(c.books, 10);
        assert_eq!(c.authors, 200_000);
        assert!(!c.truncate);
        assert!(SeedConfig::from_map(&env(&[("DATABASE_URL", "x"), ("SEED_TRUNCATE", "maybe")])).is_err());
        assert!(SeedConfig::from_map(&env(&[("DATABASE_URL", "x"), ("SEED_AUTHORS", "0")])).is_err());
        assert!(SeedConfig::from_map(&env(&[("DATABASE_URL", "x"), ("SEED_WORKERS", "0")])).is_err());
    }
}
