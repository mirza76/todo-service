# 0002. PostgreSQL as the shared store for multiple replicas

- **Status:** Accepted
- **Date:** 2026-10-05

## Context

The challenge allows in-memory storage or an embedded store (e.g. BoltDB,
optionally on a PVC), and requires a Deployment with **2 replicas**. These
requirements conflict:

- **In-memory:** each replica holds a separate dataset. Behind a load-balanced
  Service, a client can create a todo and then fail to read it back, and every
  restart loses data.
- **BoltDB on a PVC:** BoltDB holds an exclusive `flock` on its file, so only
  one process can open it. Common volume types are `ReadWriteOnce`, so two
  pods on different nodes can't mount the same volume anyway.
- **BoltDB per pod (StatefulSet):** each replica has different data, the same
  inconsistency as in-memory.

## Decision

Keep storage behind the `todo.Repository` port, with two adapters:

- **memory:** the default for `go run` and tests. Zero setup.
- **postgres:** used in Kubernetes. All replicas share one consistent,
  durable dataset.

For the dev overlay, PostgreSQL runs in-cluster as a StatefulSet with a PVC,
packaged as an optional Kustomize component so production can use a managed
database instead.

## Alternatives considered

- **Run 1 replica with BoltDB:** consistent, but violates the requirement and
  gives no high availability.
- **Replicated embedded store (rqlite, dqlite, Raft):** keeps the "embedded"
  spirit but adds significant complexity and operational risk for little gain.
- **SQLite:** has the same single-writer, single-node limitation as BoltDB.

## Consequences

- Stateless API pods: they can be scaled, rolled, and killed freely, which the
  smoke-test failure drills check.
- A new runtime dependency (PostgreSQL) and its failure modes. These are
  handled by connect-retry at startup, readiness checks, and per-request
  timeouts.
- The in-memory adapter remains useful but is documented as not shared between
  replicas; the service logs a warning when it is used.
