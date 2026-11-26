# ---------- build stage ----------
FROM rust:1.100-slim-bookworm AS builder
WORKDIR /src
# 1) dependencies layer (cached unless Cargo.toml/Cargo.lock change)
COPY Cargo.toml Cargo.lock ./
RUN mkdir src && echo 'fn main() {}' > src/main.rs && touch src/lib.rs \
    && cargo build --release --locked \
    && rm -rf src target/release/deps/world_library* target/release/deps/libworld_library* \
       target/release/world-library* target/release/.fingerprint/world_library*
# 2) application
COPY migrations ./migrations
COPY openapi.yaml ./
COPY src ./src
RUN touch src/main.rs src/lib.rs && cargo build --release --locked

# ---------- runtime stage: distroless, non-root, no shell ----------
FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=builder /src/target/release/world-library /usr/local/bin/world-library
USER nonroot:nonroot
ENV HTTP_ADDR=0.0.0.0:8080 LOG_LEVEL=info
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 \
    CMD ["/usr/local/bin/world-library", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/world-library"]
CMD ["serve"]
