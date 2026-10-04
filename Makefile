SHELL := /bin/bash
.DEFAULT_GOAL := help

APP        := todo-api
PKG        := github.com/mirza76/todo-service
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS    := -s -w -X main.version=$(VERSION)
COVER_FILE := coverage.out

# Testcontainers does not read the active Docker context, so point it at the
# current context's socket (e.g. Colima). Inside the Docker VM the socket is
# at the standard path, which the Ryuk cleanup container needs.
DOCKER_HOST ?= $(shell docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null)
TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE ?= /var/run/docker.sock
export DOCKER_HOST TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## ---------- Development ----------

.PHONY: run
run: ## Run the API locally (in-memory storage)
	go run ./cmd/$(APP)

.PHONY: build
build: ## Build the binary into ./bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(APP) ./cmd/$(APP)

.PHONY: tidy
tidy: ## Tidy and verify go modules
	go mod tidy
	go mod verify

## ---------- Quality ----------

.PHONY: fmt
fmt: ## Format code
	golangci-lint fmt ./...

.PHONY: lint
lint: ## Run linters
	golangci-lint run ./...

.PHONY: test
test: ## Run all tests incl. PostgreSQL integration tests (needs Docker)
	go test -race -count=1 ./...

.PHONY: test-unit
test-unit: ## Run unit tests only (no Docker needed)
	go test -race -count=1 -short ./...

.PHONY: cover
cover: ## Run tests and print a coverage summary
	go test -race -count=1 -covermode=atomic -coverpkg=./... -coverprofile=$(COVER_FILE) ./...
	go tool cover -func=$(COVER_FILE) | tail -n 1

.PHONY: check
check: lint test ## Run everything CI runs (lint + test)
