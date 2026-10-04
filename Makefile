SHELL := /bin/bash
.DEFAULT_GOAL := help

APP        := todo-api
PKG        := github.com/mirza76/todo-service
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS    := -s -w -X main.version=$(VERSION)
COVER_FILE := coverage.out

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
test: ## Run unit tests with the race detector
	go test -race -count=1 ./...

.PHONY: cover
cover: ## Run tests and print a coverage summary
	go test -race -count=1 -covermode=atomic -coverprofile=$(COVER_FILE) ./...
	go tool cover -func=$(COVER_FILE) | tail -n 1

.PHONY: check
check: lint test ## Run everything CI runs (lint + test)
