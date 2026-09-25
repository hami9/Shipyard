# Shipyard developer tasks. See docs/DEVELOPMENT.md.

SHELL := /bin/bash
.DEFAULT_GOAL := help

GO      ?= go
BIN_DIR ?= bin
PKG     := github.com/hami9/shipyard
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null)
LDFLAGS := -s -w -X $(PKG)/internal/buildinfo.version=$(VERSION) -X $(PKG)/internal/buildinfo.commit=$(COMMIT)

# staticcheck 2026.2.1. It cannot yet analyze Go 1.27, hence the go1.26 toolchain pin in go.mod.
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.1

# GoReleaser is installed as a binary, not `go run`, because it needs a newer Go
# than go.mod pins and `go run` would pass that toolchain on to the release build.
GORELEASER_VERSION := v2.18.2
GORELEASER         := $(BIN_DIR)/tools/goreleaser

# Local development database (deploy/dev/compose.yaml). Dev-only credentials.
DEV_COMPOSE                ?= docker compose -f deploy/dev/compose.yaml
SHIPYARD_DATABASE_URL      ?= postgres://shipyard:shipyard@127.0.0.1:54320/shipyard?sslmode=disable
SHIPYARD_TEST_DATABASE_URL ?= postgres://shipyard:shipyard@127.0.0.1:54320/postgres?sslmode=disable

.PHONY: help build test lint fmt test-integration dev-up dev-down dev-reset migrate run-api run-worker release-check release-snapshot clean

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-18s %s\n", $$1, $$2}'

build: ## Build static binaries into ./bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/ ./cmd/...

test: ## Unit tests with the race detector
	$(GO) test -race ./...

lint: ## gofmt check, go vet, staticcheck (unit and integration files)
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi
	$(GO) vet ./...
	$(GO) vet -tags integration ./...
	$(GO) run $(STATICCHECK) ./...
	$(GO) run $(STATICCHECK) -tags integration ./...

fmt: ## Format all Go files
	gofmt -w .

test-integration: ## Integration tests against PostgreSQL (run `make dev-up` first)
	SHIPYARD_TEST_DATABASE_URL='$(SHIPYARD_TEST_DATABASE_URL)' $(GO) test -race -tags integration ./...

dev-up: ## Start local PostgreSQL 18 (Docker, bound to 127.0.0.1:54320)
	$(DEV_COMPOSE) up -d --wait

dev-down: ## Stop local services; keeps the data volume
	$(DEV_COMPOSE) down

dev-reset: ## Stop local services and delete their data
	$(DEV_COMPOSE) down -v

migrate: ## Apply migrations to $SHIPYARD_DATABASE_URL
	SHIPYARD_DATABASE_URL='$(SHIPYARD_DATABASE_URL)' $(GO) run ./cmd/shipyard-api migrate

run-api: ## Run the API against the dev database (127.0.0.1:8080)
	SHIPYARD_DATABASE_URL='$(SHIPYARD_DATABASE_URL)' SHIPYARD_LOG_FORMAT=text $(GO) run ./cmd/shipyard-api serve

run-worker: ## Run the worker against the dev database
	SHIPYARD_DATABASE_URL='$(SHIPYARD_DATABASE_URL)' SHIPYARD_LOG_FORMAT=text $(GO) run ./cmd/shipyard-worker run

$(GORELEASER):
	GOBIN=$(CURDIR)/$(BIN_DIR)/tools $(GO) install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)

release-check: $(GORELEASER) ## Validate .goreleaser.yaml
	$(GORELEASER) check

release-snapshot: $(GORELEASER) ## Build release archives and images locally into ./dist (nothing is published)
	env -u GOTOOLCHAIN $(GORELEASER) release --snapshot --clean

clean: ## Remove build and release output
	rm -rf $(BIN_DIR) dist
