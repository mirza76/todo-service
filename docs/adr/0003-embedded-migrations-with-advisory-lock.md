# 0003. Embedded migrations serialized with an advisory lock

- **Status:** Accepted
- **Date:** 2026-10-05

## Context

The schema must exist before the service handles requests. With 2 replicas
(and more during a rolling update), several processes start at the same time
and would race to create tables.

## Decision

- SQL files live in `internal/storage/postgres/migrations/` and are compiled
  into the binary with `embed`, so the schema version always matches the code.
- At startup, `Migrate` runs **one transaction** that:
  1. takes `pg_advisory_xact_lock(<app key>)`, so other replicas block here;
  2. creates `schema_migrations` if needed;
  3. applies each unapplied file in filename order and records it.
- The lock is released automatically on commit or rollback.

## Alternatives considered

- **golang-migrate / goose:** mature tools, but a dependency plus CLI
  conventions for a single table. The core logic here is about 60 lines and
  easy to audit.
- **A Kubernetes Job or init container for migrations:** a clean separation,
  but it adds ordering concerns and more manifests. Worth revisiting if
  migrations become long-running.

## Consequences

- Concurrent startup is safe. A test runs 5 concurrent migrators against a
  fresh database and asserts exactly one applies the schema.
- PostgreSQL DDL is transactional, so a failing migration leaves no partial
  schema.
- Migrations must stay fast and backward-compatible: during a rolling update,
  old pods run against the new schema.
- No down-migrations. Fixes roll forward with a new migration.
