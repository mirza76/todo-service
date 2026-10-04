package metrics_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/mirza76/todo-service/internal/metrics"
)

func TestInstrument(t *testing.T) {
	reg := metrics.NewRegistry("v1.2.3")
	m := metrics.NewHTTP(reg)

	h := m.Instrument("/todos/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/todos/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))

	for _, path := range []string{"/todos/a", "/todos/b", "/todos/missing"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
	}

	// Two different IDs collapse into one series labelled with the pattern.
	expected := `
# HELP http_requests_total HTTP requests processed, by method, route pattern, and status code.
# TYPE http_requests_total counter
http_requests_total{code="200",method="get",route="/todos/{id}"} 2
http_requests_total{code="404",method="get",route="/todos/{id}"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "http_requests_total"); err != nil {
		t.Error(err)
	}

	if n := testutil.CollectAndCount(reg, "http_request_duration_seconds"); n != 1 {
		t.Errorf("duration series = %d, want 1 (method+route only)", n)
	}

	build := `
# HELP todo_api_build_info Build information; the value is always 1.
# TYPE todo_api_build_info gauge
todo_api_build_info{version="v1.2.3"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(build), "todo_api_build_info"); err != nil {
		t.Error(err)
	}
}

func TestHandlerExposesRuntimeMetrics(t *testing.T) {
	reg := metrics.NewRegistry("dev")
	metrics.NewHTTP(reg)

	rec := httptest.NewRecorder()
	metrics.Handler(reg).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, name := range []string{"go_goroutines", "process_resident_memory_bytes", "http_requests_in_flight", "todo_api_build_info"} {
		if !strings.Contains(body, name) {
			t.Errorf("/metrics output is missing %s", name)
		}
	}
}
