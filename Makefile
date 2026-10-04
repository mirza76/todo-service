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

## ---------- Docker ----------

IMAGE ?= todo-api:$(VERSION)

.PHONY: docker-build
docker-build: ## Build the container image
	docker buildx build --load --build-arg VERSION=$(VERSION) -t $(IMAGE) -t todo-api:latest .

.PHONY: docker-run
docker-run: ## Run the image locally on :8080 (in-memory storage)
	docker run --rm -p 8080:8080 --name todo-api $(IMAGE)

## ---------- Kubernetes (kind) ----------

KIND_CLUSTER := todo
K8S_OVERLAY  := deploy/k8s/overlays/dev
KUBECTL      := kubectl --context kind-$(KIND_CLUSTER)

.PHONY: kind-up
kind-up: ## Create the local kind cluster (1 control plane + 2 workers)
	kind get clusters | grep -qx $(KIND_CLUSTER) || kind create cluster --config deploy/kind/cluster.yaml

.PHONY: kind-load
kind-load: docker-build ## Build the image and load it into kind as todo-api:dev
	docker tag $(IMAGE) todo-api:dev
	kind load docker-image todo-api:dev --name $(KIND_CLUSTER)

.PHONY: deploy
deploy: kind-load ## Build, load, and deploy to kind; waits for rollout
	@# The image tag (:dev) doesn't change between builds, so an existing
	@# Deployment must be restarted to pick up the new image. A fresh one
	@# already runs it.
	@existed=$$($(KUBECTL) -n todo get deployment todo-api -o name 2>/dev/null); \
	$(KUBECTL) apply -k $(K8S_OVERLAY) && \
	if [ -n "$$existed" ]; then $(KUBECTL) -n todo rollout restart deployment/todo-api; fi
	$(KUBECTL) -n todo rollout status statefulset/postgres --timeout=180s
	$(KUBECTL) -n todo rollout status deployment/todo-api --timeout=180s

.PHONY: smoke
smoke: ## Run the end-to-end smoke test against the kind deployment
	KUBECTL="$(KUBECTL)" ./scripts/smoke.sh

.PHONY: undeploy
undeploy: ## Remove the app (and its data) from kind
	$(KUBECTL) delete -k $(K8S_OVERLAY) --ignore-not-found
	$(KUBECTL) -n todo delete pvc --all --ignore-not-found

.PHONY: kind-down
kind-down: ## Delete the kind cluster
	kind delete cluster --name $(KIND_CLUSTER)

.PHONY: check
check: lint test ## Run everything CI runs (lint + test)
