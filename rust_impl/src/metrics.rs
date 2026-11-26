//! Prometheus metrics (own registry, no process-global state).

use prometheus::{Encoder, HistogramOpts, HistogramVec, IntCounterVec, IntGauge, Opts, Registry, TextEncoder};

pub struct Metrics {
    registry: Registry,
    pub http_requests: IntCounterVec,
    pub http_duration: HistogramVec,
    pub db_pool_size: IntGauge,
    pub db_pool_idle: IntGauge,
}

impl Metrics {
    pub fn new() -> anyhow::Result<Self> {
        let registry = Registry::new_custom(Some("world_library".into()), None)?;
        let http_requests =
            IntCounterVec::new(Opts::new("http_requests_total", "HTTP requests by route and status"), &["method", "route", "status"])?;
        let http_duration = HistogramVec::new(
            HistogramOpts::new("http_request_duration_seconds", "HTTP request latency")
                .buckets(vec![0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0]),
            &["method", "route"],
        )?;
        let db_pool_size = IntGauge::new("db_pool_connections", "Open connections in the pool")?;
        let db_pool_idle = IntGauge::new("db_pool_idle_connections", "Idle connections in the pool")?;
        registry.register(Box::new(http_requests.clone()))?;
        registry.register(Box::new(http_duration.clone()))?;
        registry.register(Box::new(db_pool_size.clone()))?;
        registry.register(Box::new(db_pool_idle.clone()))?;
        Ok(Self { registry, http_requests, http_duration, db_pool_size, db_pool_idle })
    }

    pub fn observe(&self, method: &str, route: &str, status: u16, secs: f64) {
        self.http_requests.with_label_values(&[method, route, &status.to_string()]).inc();
        self.http_duration.with_label_values(&[method, route]).observe(secs);
    }

    pub fn render(&self) -> String {
        let mut buf = Vec::new();
        let _ = TextEncoder::new().encode(&self.registry.gather(), &mut buf);
        String::from_utf8(buf).unwrap_or_default()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn render_contains_series() {
        let m = Metrics::new().unwrap();
        m.observe("GET", "/api/v1/books", 200, 0.01);
        m.db_pool_size.set(3);
        let out = m.render();
        assert!(out.contains("world_library_http_requests_total{method=\"GET\",route=\"/api/v1/books\",status=\"200\"} 1"));
        assert!(out.contains("world_library_http_request_duration_seconds_bucket"));
        assert!(out.contains("world_library_db_pool_connections 3"));
    }
}
