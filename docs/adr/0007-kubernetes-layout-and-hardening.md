# 0007. Kustomize layout, hardening, and credential handling

- **Status:** Accepted
- **Date:** 2026-10-05

## Context

Manifests must cover a Deployment (2 replicas, resource limits, probes), a
Service, a ConfigMap and/or Secrets, and optionally a PVC. They should follow
Kubernetes best practices, work locally on kind, and adapt to other
environments without copy-pasting YAML.

## Decision

**Layout (Kustomize, built into `kubectl`):**

- `base/`: the application only, with no environment-specific values and **no
  Secret**.
- `components/postgres/`: optional in-cluster PostgreSQL (StatefulSet,
  `volumeClaimTemplates`, headless Service).
- `overlays/dev/`: base + postgres + dev Secret + a NodePort Service so kind
  can expose the API on `localhost:8080`. The base ClusterIP Service is
  unchanged.

**Configuration:** `configMapGenerator` / `secretGenerator` append a content
hash to resource names. Changing a value changes the name referenced by the
pod template, which triggers a rolling restart, so config changes are never
silently ignored.

**Hardening (both the API and PostgreSQL):**

- The namespace enforces the `restricted` Pod Security Standard.
- `runAsNonRoot`, `readOnlyRootFilesystem`, `allowPrivilegeEscalation: false`,
  `capabilities.drop: [ALL]`, `seccompProfile: RuntimeDefault`.
- `automountServiceAccountToken: false`, because the app never calls the
  Kubernetes API.

**Availability:** 2 replicas with `topologySpreadConstraints` across nodes,
rolling updates with `maxUnavailable: 0`, and a PodDisruptionBudget
(`minAvailable: 1`).

**Resources:** requests sized for scheduling, limits per the requirement.
`GOMEMLIMIT` comes from `limits.memory` through the Downward API, so the Go
GC adapts to the container limit. `GOMAXPROCS` already follows the CPU limit
automatically since Go 1.25.

**Credentials:** the dev overlay commits a clearly-labelled throwaway
password. Real environments must provide the `todo-db` Secret (`username`,
`password`) from a secret manager, e.g. External Secrets Operator or Sealed
Secrets.

## Alternatives considered

- **Helm:** better for distributing a configurable package to third parties,
  but templating adds indirection. Kustomize keeps manifests as plain,
  reviewable YAML.
- **Plain YAML per environment:** duplication drifts over time.
- **ClusterIP + `kubectl port-forward` for local access:** port-forward pins
  one pod, which hides load balancing and breaks when that pod is killed during
  failure drills.

## Consequences

- `kubectl apply -k deploy/k8s/overlays/dev` is the single deploy command.
- `base` alone isn't deployable without an overlay that supplies the Secret.
  This is intentional: credentials are always environment-specific.
- The in-cluster PostgreSQL is a single instance, fine for development but not
  highly available.
