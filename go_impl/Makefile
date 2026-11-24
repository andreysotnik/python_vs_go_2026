# World Library (Go) — developer entry points. Everything runs in Docker; no local Go required.
COMPOSE      ?= docker compose
NETWORK      ?= lib_go_default
LINT_IMAGE   ?= golangci/golangci-lint:v2.12.2
K6_IMAGE     ?= grafana/k6:1.3.0
BENCH_VUS    ?= 32
BENCH_DUR    ?= 60s

.PHONY: up migrate seed test test-local lint lint-offline explain bench down logs build vendor

up:            ## build images, start postgres, run migrations, start the app (waits for health)
	$(COMPOSE) up -d --build --wait

build:
	$(COMPOSE) build

migrate:       ## apply migrations (separate one-shot service)
	$(COMPOSE) run --rm migrate

seed:          ## load mock data (SEED_BOOKS / SEED_AUTHORS / SEED_RANDOM ...)
	$(COMPOSE) --profile seed run --rm seed

test:          ## unit + integration tests against the compose postgres, with coverage
	$(COMPOSE) --profile test run --rm test

test-local:    ## same, with a local Go toolchain (needs `make up`)
	TEST_DATABASE_URL='postgres://library:library@localhost:25432/postgres?sslmode=disable' \
	go test -count=1 -p 1 -covermode=atomic \
	  -coverpkg=$$(go list ./internal/... | grep -v -e /seed -e /explain -e /dbx -e /migrate -e /integration | paste -sd, -) \
	  -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -n 1

lint:          ## golangci-lint (gofmt, goimports, govet, staticcheck, gosec, errorlint, revive, ...)
	docker run --rm -v "$(CURDIR)":/src -w /src -e GOFLAGS=-mod=vendor -e GOTOOLCHAIN=local $(LINT_IMAGE) golangci-lint run ./...

lint-offline:  ## lint without pulling images: builds golangci-lint from the local Go module cache in tmpfs
	docker run --rm --tmpfs /tmp:size=3g,exec -v "$$HOME/go/pkg/mod/cache/download":/proxy:ro -v "$(CURDIR)":/src:ro -w /src \
	  -e GOPROXY=file:///proxy -e GOSUMDB=off -e GOTOOLCHAIN=local -e CGO_ENABLED=0 -e GOMODCACHE=/tmp/mod -e GOCACHE=/tmp/gocache \
	  -e GOPATH=/tmp/gopath -e HOME=/tmp -e GOLANGCI_LINT_CACHE=/tmp/lintcache golang:1.26-alpine \
	  sh -c 'GOFLAGS=-mod=mod GOBIN=/tmp/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 && GOFLAGS=-mod=vendor /tmp/bin/golangci-lint run ./...'

explain:       ## EXPLAIN (ANALYZE, BUFFERS) key queries on the seeded DB; fails on Seq Scan / OFFSET
	$(COMPOSE) run --rm --no-deps -T app explain > docs/QUERY_PLANS.md

bench:         ## k6 load test against the running app; summary in loadtest/results/
	mkdir -p loadtest/results
	docker run --rm --network $(NETWORK) -e BASE_URL=http://app:8080 -e VUS=$(BENCH_VUS) -e DURATION=$(BENCH_DUR) \
	  -v "$(CURDIR)/loadtest":/scripts $(K6_IMAGE) run --summary-trend-stats="avg,med,p(95),p(99),max" /scripts/load.js

logs:
	$(COMPOSE) logs -f app

vendor:
	go mod tidy && go mod vendor

down:          ## stop everything (volumes are kept; add -v manually to drop data)
	$(COMPOSE) --profile seed --profile test down
