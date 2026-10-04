# 0001. Layered (hexagonal) architecture on the Go standard library

- **Status:** Accepted
- **Date:** 2026-10-05

## Context

The service is small, but it is judged on code quality and structure and
must support more than one storage backend (see [0002](0002-postgresql-for-shared-state.md)).
Business rules should be testable without HTTP or a database, and storage
should be swappable without touching handlers.

## Decision

Organize code in layers with dependencies pointing inward:

| Package | Responsibility | Depends on |
|---|---|---|
| `internal/todo` | Entity, invariants, domain errors, `Repository` port | nothing in the project |
| `internal/service` | Use cases; IDs, clock, pagination rules | `todo` |
| `internal/storage/*` | `Repository` adapters (memory, postgres) | `todo` |
| `internal/httpapi` | HTTP translation only | `service` (via a consumer-side interface), `todo` |
| `cmd/todo-api` | Composition root: wiring and lifecycle | everything |

Use the standard library wherever it is sufficient: `net/http.ServeMux`
(method and `{id}` patterns since Go 1.22), `log/slog`, `encoding/json`, and
`os.LookupEnv`. Dependency injection is done by hand in `main`.

## Alternatives considered

- **A web framework (Gin, Echo, chi):** offers convenience, but the standard
  router now covers everything this API needs. A framework would add
  dependency surface and hide behavior a reviewer would want to see.
- **A DI container (wire, fx):** too much machinery for about ten objects;
  explicit wiring in `main` is easier to read.
- **A flat package:** simplest, but mixes HTTP, SQL, and rules, and makes the
  storage swap and the shared contract tests harder.

## Consequences

- Each layer is tested in isolation, and the storage contract suite runs
  unchanged against every adapter.
- `httpapi` defines the `TodoService` interface it consumes, so handler tests
  can inject failures (500s, panics, timeouts) with a small stub.
- The design has slightly more files than a flat layout would, which is
  acceptable for the clarity gained.
