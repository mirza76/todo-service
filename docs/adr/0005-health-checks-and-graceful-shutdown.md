# 0005. Health checks and graceful shutdown

- **Status:** Accepted
- **Date:** 2026-10-05

## Context

The challenge requires readiness and liveness probes and graceful handling of
restarts and failures. Two Kubernetes behaviors matter:

1. A failing **liveness** probe restarts the container. A failing
   **readiness** probe only removes the pod from Service endpoints.
2. On pod termination, endpoint removal and `SIGTERM` happen **concurrently**.
   kube-proxy on every node needs a moment to stop routing to the pod, so
   requests can still arrive after the signal.

## Decision

**Probes**

| Endpoint | Checks | Used by |
|---|---|---|
| `/livez` | Nothing beyond "the server answers" | startupProbe, livenessProbe |
| `/readyz` | PostgreSQL ping (2 s timeout) and shutdown flag | readinessProbe |

Liveness deliberately ignores the database: restarting the app cannot fix a
database outage, and coupling the two causes restart storms.

**Startup:** the app retries the database with exponential backoff (250 ms,
doubling, max 5 s) for up to `DB_CONNECT_TIMEOUT` (30 s), runs migrations, and
only then listens. If the database never becomes reachable, it **exits with a
clear error** and Kubernetes restarts it with backoff. The startupProbe allows
60 s before liveness takes over.

**Shutdown sequence** on `SIGTERM`/`SIGINT`:

1. Mark not-ready: `/readyz` returns `503 shutting_down`.
2. Keep serving for `SHUTDOWN_DELAY` (5 s) so endpoint removal propagates.
3. `http.Server.Shutdown`: stop accepting connections and wait up to
   `SHUTDOWN_TIMEOUT` (15 s) for in-flight requests.
4. Close the database pool and exit 0.

5 s + 15 s fits inside `terminationGracePeriodSeconds: 30`. The delay lives in
the app, not a `preStop` `sleep` hook, because the distroless image has no
shell or `sleep` binary.

## Alternatives considered

- **Database check in liveness:** rejected, as above.
- **Start serving before the database is ready (readiness false):** avoids the
  first-deploy restart, but needs lazy initialization and a half-initialized
  state in the app. Fail-fast is simpler and the orchestrator already handles
  restarts.
- **`preStop: sleep`:** needs a shell in the image, or the newer native sleep
  action. The in-app delay works everywhere, including `docker stop`.

## Consequences

- Measured on kind under continuous load: a rolling restart had 0 failed
  requests with the 5 s delay, versus 2 of 153 failed with `SHUTDOWN_DELAY=0s`.
- Pod termination takes about 5 s longer. That's an acceptable trade-off.
- On a brand-new cluster, PostgreSQL's first start (image pull + `initdb`) can
  exceed 30 s, so an API pod may restart once before connecting. This is
  expected and documented.
