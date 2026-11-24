# World Library (Python) - developer entry points.
# Host tools: Docker + docker compose v2; `uv` for test/lint targets.
SHELL := /bin/bash
COMPOSE := docker compose
UV := env -u HTTPS_PROXY -u HTTP_PROXY uv
HOST_DB_URL := postgresql://library:library@localhost:15432/library
SEED_BOOKS ?= 1000000
SEED_AUTHORS ?= 200000
SEED_RANDOM ?= 42

.PHONY: up migrate seed test test-unit test-integration explain lint fmt bench openapi down logs build

build:            ## build the application image
	$(COMPOSE) build migrate

up:               ## build + start postgres, run migrations, start the API on :18001
	$(COMPOSE) up -d --build --wait app

migrate:          ## apply migrations (separate one-shot service)
	$(COMPOSE) run --rm migrate

seed:             ## bulk-load mock data (SEED_BOOKS / SEED_AUTHORS / SEED_RANDOM)
	SEED_BOOKS=$(SEED_BOOKS) SEED_AUTHORS=$(SEED_AUTHORS) SEED_RANDOM=$(SEED_RANDOM) $(COMPOSE) run --rm seed

test:             ## unit + integration tests (PostgreSQL via testcontainers) with coverage
	$(UV) run pytest tests/unit tests/integration --cov --cov-report=term --cov-fail-under=80

test-unit:
	$(UV) run pytest tests/unit

test-integration:
	$(UV) run pytest tests/integration

explain:          ## EXPLAIN (ANALYZE, BUFFERS) checks on the seeded compose DB -> docs/QUERY_PLANS.md
	PERF_DATABASE_URL=$(HOST_DB_URL) $(UV) run pytest tests/perf -v

lint:             ## ruff (lint + format check) and mypy --strict
	$(UV) run ruff check src tests migrations
	$(UV) run ruff format --check src tests migrations
	$(UV) run mypy

fmt:
	$(UV) run ruff format src tests migrations
	$(UV) run ruff check --fix src tests migrations

openapi:          ## regenerate openapi.yaml from the code
	$(UV) run python -m library.openapi_export openapi.yaml

bench:            ## k6 load test against the running app (results in bench/results/)
	mkdir -p bench/results
	$(COMPOSE) --profile bench run --rm k6 2>&1 | tee bench/results/k6-output.txt

logs:
	$(COMPOSE) logs -f app

down:             ## stop everything (data stays in ./.pgdata)
	$(COMPOSE) --profile tools --profile bench down
