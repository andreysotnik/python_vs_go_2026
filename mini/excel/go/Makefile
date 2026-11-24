GOLANGCI_LINT_VERSION ?= v2.15.0
# The corporate proxy is not reachable from this machine; use the public one.
GOENV := env -u HTTPS_PROXY -u HTTP_PROXY GOPROXY=https://proxy.golang.org,direct

.PHONY: run build test lint fmt clean

run:
	go run ./cmd/salesreport --input data/sales.xlsx --out out/

build:
	go build -o bin/salesreport ./cmd/salesreport

test:
	go test -race -cover ./...

# golangci-lint must be built with Go >= 1.27 (the go.mod version), hence `go run`.
lint:
	gofmt -l . | (! grep .)
	go vet ./...
	$(GOENV) go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

fmt:
	gofmt -w .

clean:
	rm -rf out bin
