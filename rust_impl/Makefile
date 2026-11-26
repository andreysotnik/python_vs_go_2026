# World Library (Rust) — common tasks. Everything except *-local targets needs only Docker.
COMPOSE      ?= docker compose
DB_URL_HOST  ?= postgres://library:library@127.0.0.1:35432/library
CARGO_ENV    ?= env -u HTTPS_PROXY -u https_proxy -u HTTP_PROXY -u http_proxy
VUS          ?= 32
DURATION     ?= 60s

.PHONY: up down build migrate seed test test-local cover lint fmt bench explain logs ps

up:            ## build images, start postgres + migrations + app
	$(COMPOSE) up -d --build

build:
	$(COMPOSE) build migrate

migrate:       ## apply migrations (separate one-shot service)
	$(COMPOSE) run --rm migrate

seed: build    ## load mock data: SEED_BOOKS / SEED_AUTHORS / SEED_RANDOM (default 1M / 200k / 42)
	$(COMPOSE) up -d postgres
	$(COMPOSE) run --rm migrate
	$(COMPOSE) --profile seed run --rm seed

test:          ## unit + integration tests in a Rust container against compose postgres
	$(COMPOSE) up -d postgres
	$(COMPOSE) --profile test run --rm test

test-local:    ## same with the local toolchain (postgres on 127.0.0.1:35432)
	$(COMPOSE) up -d postgres
	$(CARGO_ENV) cargo test

cover:         ## coverage report (needs cargo-llvm-cov locally)
	$(COMPOSE) up -d postgres
	$(CARGO_ENV) cargo llvm-cov --summary-only

lint:          ## rustfmt + clippy (deny warnings)
	cargo fmt --check
	$(CARGO_ENV) cargo clippy --all-targets --locked -- -D warnings

fmt:
	cargo fmt

explain:       ## EXPLAIN (ANALYZE, BUFFERS) of key queries on the seeded DB -> docs/QUERY_PLANS.md
	UID=$$(id -u) GID=$$(id -g) $(COMPOSE) --profile tools run --rm explain

bench:         ## k6 load test against the running app (results in bench/results)
	docker run --rm --network lib_rust_default -v $(CURDIR)/bench:/bench -v $(CURDIR)/bench/results:/results \
		-e VUS=$(VUS) -e DURATION=$(DURATION) -e BASE_URL=http://app:8080 grafana/k6:latest run --quiet /bench/k6.js

logs:
	$(COMPOSE) logs -f app

ps:
	$(COMPOSE) ps

down:          ## stop everything (volumes are kept; add -v to drop data)
	$(COMPOSE) --profile seed --profile test --profile tools down
