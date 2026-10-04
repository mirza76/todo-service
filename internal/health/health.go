// Package health implements Kubernetes-style liveness and readiness
// endpoints.
//
//   - Liveness answers "is the process able to serve at all?" It deliberately
//     checks no dependencies: if the database is down, restarting this
//     container would not help and could cause a restart storm.
//   - Readiness answers "should this instance receive traffic right now?" It
//     runs dependency checks and reports not-ready once shutdown has begun, so
//     Kubernetes stops routing new requests before the server stops.
package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// Check reports whether a dependency is healthy.
type Check func(ctx context.Context) error

// NamedCheck pairs a Check with the name shown in readiness output.
type NamedCheck struct {
	Name  string
	Check Check
}

// Checker serves liveness and readiness endpoints.
type Checker struct {
	logger       *slog.Logger
	timeout      time.Duration
	checks       []NamedCheck
	shuttingDown atomic.Bool
}

// New returns a Checker. timeout bounds each readiness evaluation so a hung
// dependency cannot hang the probe.
func New(logger *slog.Logger, timeout time.Duration, checks ...NamedCheck) *Checker {
	return &Checker{logger: logger, timeout: timeout, checks: checks}
}

// SetShuttingDown marks the instance as not ready. It is irreversible.
func (c *Checker) SetShuttingDown() {
	c.shuttingDown.Store(true)
}

type response struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// Live handles liveness probes.
func (c *Checker) Live(w http.ResponseWriter, _ *http.Request) {
	c.write(w, http.StatusOK, response{Status: "ok"})
}

// Ready handles readiness probes. Failure details are logged, not returned,
// to avoid leaking infrastructure information to callers.
func (c *Checker) Ready(w http.ResponseWriter, r *http.Request) {
	if c.shuttingDown.Load() {
		c.write(w, http.StatusServiceUnavailable, response{Status: "shutting_down"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), c.timeout)
	defer cancel()

	status, code := "ok", http.StatusOK
	results := make(map[string]string, len(c.checks))
	for _, nc := range c.checks {
		if err := nc.Check(ctx); err != nil {
			c.logger.WarnContext(ctx, "readiness check failed", slog.String("check", nc.Name), slog.Any("error", err))
			results[nc.Name] = "fail"
			status, code = "unavailable", http.StatusServiceUnavailable
			continue
		}
		results[nc.Name] = "ok"
	}
	c.write(w, code, response{Status: status, Checks: results})
}

func (c *Checker) write(w http.ResponseWriter, code int, body response) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		c.logger.Warn("write health response", slog.Any("error", err))
	}
}
