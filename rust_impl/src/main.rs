//! Single binary with sub-commands: `serve` (default), `migrate`, `seed`, `explain`.

use world_library::{config, db, explain, init_logging, seed, serve};

fn usage() -> ! {
    eprintln!("usage: world-library [serve|migrate|seed|explain [output.md]|healthcheck]");
    std::process::exit(2);
}

/// Container HEALTHCHECK for the distroless image (no curl/wget there).
async fn healthcheck() -> anyhow::Result<()> {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    let addr = std::env::var("HTTP_ADDR").unwrap_or_else(|_| "0.0.0.0:8080".into());
    let port = addr.rsplit(':').next().unwrap_or("8080");
    let fut = async {
        let mut s = tokio::net::TcpStream::connect(format!("127.0.0.1:{port}")).await?;
        s.write_all(b"GET /healthz HTTP/1.0\r\nHost: localhost\r\n\r\n").await?;
        let mut buf = Vec::new();
        s.read_to_end(&mut buf).await?;
        anyhow::ensure!(buf.starts_with(b"HTTP/1.0 200") || buf.starts_with(b"HTTP/1.1 200"), "unhealthy");
        Ok(())
    };
    tokio::time::timeout(std::time::Duration::from_secs(3), fut).await?
}

#[tokio::main]
async fn main() {
    let args: Vec<String> = std::env::args().collect();
    let cmd = args.get(1).map_or("serve", String::as_str);
    init_logging(&std::env::var("LOG_LEVEL").unwrap_or_else(|_| "info".into()));
    let result = match cmd {
        "serve" => match config::Config::from_env() {
            Ok(cfg) => serve(cfg).await,
            Err(e) => Err(e),
        },
        "migrate" => match std::env::var("DATABASE_URL") {
            Ok(url) => db::migrate(&url).await.map(|()| tracing::info!("migrations applied")),
            Err(_) => Err(anyhow::anyhow!("DATABASE_URL is required")),
        },
        "seed" => match config::SeedConfig::from_env() {
            Ok(cfg) => seed::run(cfg).await,
            Err(e) => Err(e),
        },
        "explain" => match std::env::var("DATABASE_URL") {
            Ok(url) => explain::run(&url, args.get(2).map(String::as_str)).await,
            Err(_) => Err(anyhow::anyhow!("DATABASE_URL is required")),
        },
        "healthcheck" => healthcheck().await,
        "-h" | "--help" | "help" => usage(),
        _ => usage(),
    };
    if let Err(e) = result {
        tracing::error!(error = %format!("{e:#}"), command = cmd, "command failed");
        eprintln!("error: {e:#}");
        std::process::exit(1);
    }
}
