package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mirza76/todo-service/internal/health"
)

var discard = slog.New(slog.DiscardHandler)

type body struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

func call(t *testing.T, h http.HandlerFunc) (int, body) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	var b body
	if err := json.NewDecoder(rec.Body).Decode(&b); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return rec.Code, b
}

func ok(context.Context) error { return nil }

func TestLive_IgnoresDependencies(t *testing.T) {
	failing := health.NamedCheck{Name: "db", Check: func(context.Context) error { return errors.New("down") }}
	c := health.New(discard, time.Second, failing)

	code, b := call(t, c.Live)
	if code != http.StatusOK || b.Status != "ok" {
		t.Errorf("Live() = %d %q, want 200 ok", code, b.Status)
	}
}

func TestReady(t *testing.T) {
	tests := []struct {
		name       string
		checks     []health.NamedCheck
		wantCode   int
		wantStatus string
		wantChecks map[string]string
	}{
		{
			name:       "no checks",
			wantCode:   http.StatusOK,
			wantStatus: "ok",
		},
		{
			name:       "all checks pass",
			checks:     []health.NamedCheck{{Name: "db", Check: ok}},
			wantCode:   http.StatusOK,
			wantStatus: "ok",
			wantChecks: map[string]string{"db": "ok"},
		},
		{
			name: "one check fails",
			checks: []health.NamedCheck{
				{Name: "db", Check: func(context.Context) error { return errors.New("connection refused") }},
				{Name: "cache", Check: ok},
			},
			wantCode:   http.StatusServiceUnavailable,
			wantStatus: "unavailable",
			wantChecks: map[string]string{"db": "fail", "cache": "ok"},
		},
		{
			name: "hung check is bounded by timeout",
			checks: []health.NamedCheck{{Name: "db", Check: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}}},
			wantCode:   http.StatusServiceUnavailable,
			wantStatus: "unavailable",
			wantChecks: map[string]string{"db": "fail"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := health.New(discard, 50*time.Millisecond, tt.checks...)
			code, b := call(t, c.Ready)

			if code != tt.wantCode || b.Status != tt.wantStatus {
				t.Errorf("Ready() = %d %q, want %d %q", code, b.Status, tt.wantCode, tt.wantStatus)
			}
			for name, want := range tt.wantChecks {
				if b.Checks[name] != want {
					t.Errorf("checks[%q] = %q, want %q", name, b.Checks[name], want)
				}
			}
		})
	}
}

func TestReady_ShuttingDown(t *testing.T) {
	c := health.New(discard, time.Second, health.NamedCheck{Name: "db", Check: ok})
	c.SetShuttingDown()

	code, b := call(t, c.Ready)
	if code != http.StatusServiceUnavailable || b.Status != "shutting_down" {
		t.Errorf("Ready() = %d %q, want 503 shutting_down", code, b.Status)
	}

	// Liveness must stay healthy during shutdown, or Kubernetes would kill
	// the pod before in-flight requests drain.
	if code, _ := call(t, c.Live); code != http.StatusOK {
		t.Errorf("Live() during shutdown = %d, want 200", code)
	}
}
