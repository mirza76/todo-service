# 0008. Optimistic concurrency with versions, ETag, and If-Match

- **Status:** Accepted
- **Date:** 2026-10-05

## Context

Updates are read-modify-write: the service reads a todo, applies the change,
and writes it back. With two replicas, two requests for the same todo can run
at the same moment on different pods. Both read the same state, and the second
write silently discards the first: a **lost update**. This affects PATCH most,
because two clients changing *different* fields expect both changes to
survive.

## Decision

1. **Version column.** Each todo has a `version` (starts at 1, +1 on every
   change, added by migration `0003` with `DEFAULT 1` so existing rows and
   old pods keep working during a rolling update).
2. **Compare-and-set in the repository.** `Update` and conditional `Delete`
   write only if the stored version equals the version that was read:
   `UPDATE ... WHERE id = $1 AND version = $expected`. A row-level atomic
   statement in PostgreSQL decides the winner, regardless of which replica
   the writers run on. If no row matched, a follow-up existence check tells
   `ErrNotFound` apart from `ErrVersionConflict`.
3. **HTTP preconditions (RFC 9110).** Responses carry a strong
   `ETag: "<version>"`. `If-Match` on PUT/PATCH/DELETE makes the write
   conditional: a mismatch, including a race lost between read and write, is
   `412 Precondition Failed`. Weak or malformed tags never match (strong
   comparison); `*` matches any version.
4. **Server-side retry for unconditional writes.** Without `If-Match`, a lost
   compare-and-set re-runs the whole read-modify-write on the fresh state, up
   to 3 attempts, then returns `409 Conflict`. Concurrent PATCHes of different
   fields therefore merge instead of overwriting each other.

## Alternatives considered

- **Last-write-wins (no versioning):** simplest, but loses updates silently.
- **Pessimistic locking (`SELECT ... FOR UPDATE`):** correct, but holds row
  locks across application logic and needs a transaction spanning the
  read-modify-write. Optimistic control fits a low-contention workload better
  and gives clients a standard HTTP mechanism (`If-Match`).
- **Single SQL statement for PATCH (`SET completed = COALESCE($x, completed)`):**
  avoids the race for PATCH but bypasses domain validation in Go and doesn't
  help PUT, DELETE, or client-side preconditions.
- **Require `If-Match` on every write (428 Precondition Required):** the
  safest option for clients, but it breaks the plain CRUD usage the challenge
  describes. Left optional.

## Consequences

- No lost updates across replicas. A contract test races 10 writers on one
  version against both adapters (exactly one wins), and the smoke test sends
  30 concurrent PATCHes through the kind Service and checks the version
  advanced by exactly the number of successes.
- Under heavy contention on a single todo, some unconditional writes exhaust
  their retries and get `409` (3 of 30 in testing). Clients should retry;
  jittered backoff between attempts would reduce this.
- Every successful write costs one extra round-trip (the read), which already
  existed for validation.
