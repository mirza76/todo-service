# 0004. RFC 9457 errors and status code semantics

- **Status:** Accepted
- **Date:** 2026-10-05

## Context

Clients need errors that are consistent, machine-readable, and precise enough
to act on, without exposing internal details.

## Decision

- **Format:** every error is RFC 9457 Problem Details
  (`application/problem+json`) with `type: about:blank`, the standard status
  `title`, a human `detail`, the request path as `instance`, and, for
  validation failures, an `errors` array of `{field, message}`. The mux's
  built-in 404/405 plain-text responses are converted to the same format.
- **One mapping point:** `httpapi.writeError` turns domain and infrastructure
  errors into statuses using `errors.Is` / `errors.As`. Handlers never choose
  error status codes themselves.
- **Status semantics:**
  - `400`: the request can't be parsed (malformed JSON, wrong type, unknown
    field, trailing data, non-integer query parameter).
  - `422`: it parses but breaks a rule (missing required field, invalid title,
    `limit` out of range). Every failing field is reported.
  - `404`: unknown or **malformed** ID. An ID that can't exist identifies no
    resource, and clients shouldn't depend on the ID format.
  - `413`, `415`: body too large; wrong content type.
  - `503`: request deadline exceeded.
  - `500`: anything unexpected. Full details are logged; the client gets a
    generic message.
- **PUT is a full replacement** (RFC 9110 §9.3.4): all client-controlled fields
  are required.
- **PATCH is JSON Merge Patch** (RFC 7396): absent fields are unchanged; an
  explicit `null` (merge-patch "remove") is rejected with 422, since neither
  field can be removed; a non-object body (including bare `null`) is a 400.
  An empty patch is a no-op.

## Alternatives considered

- **A custom `{ "error": "..." }` body:** simpler, but non-standard and
  without structure for field errors.
- **400 for all client errors:** common, but blurs "fix your encoding" and
  "fix your data".
- **400 for a malformed ID:** more explicit, but it exposes the ID format as
  part of the contract.

## Consequences

- Clients can handle errors generically by `status`, and display `errors[]`
  next to form fields.
- Unknown JSON fields are rejected, which catches client typos (`"complete"`
  vs `"completed"`) but means new optional fields must be added to the server
  before clients send them.
