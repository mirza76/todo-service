# syntax=docker/dockerfile:1

# ---- Build stage ---------------------------------------------------------
# Official Go image. Runs on the build host's native platform and
# cross-compiles, which is much faster than emulating the target platform.
ARG GO_VERSION=1.27
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

WORKDIR /src

# Download dependencies in their own layer so they are cached until
# go.mod/go.sum change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY cmd/ cmd/
COPY internal/ internal/

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
# CGO disabled -> fully static binary that runs on a distroless/static base.
# -trimpath and -s -w strip local paths and debug symbols (smaller, reproducible).
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/todo-api ./cmd/todo-api

# ---- Runtime stage -------------------------------------------------------
# Distroless static: no shell, no package manager, CA certs + tzdata only.
# Minimal attack surface; the :nonroot tag runs as UID 65532.
FROM gcr.io/distroless/static-debian13:nonroot

ARG VERSION=dev
LABEL org.opencontainers.image.title="todo-api" \
      org.opencontainers.image.description="ToDo REST microservice" \
      org.opencontainers.image.source="https://github.com/mirza76/todo-service" \
      org.opencontainers.image.version="${VERSION}"

COPY --from=build /out/todo-api /todo-api

USER 65532:65532
EXPOSE 8080

# Exec form: the binary is PID 1 and receives SIGTERM directly, which the
# graceful shutdown logic depends on.
ENTRYPOINT ["/todo-api"]
