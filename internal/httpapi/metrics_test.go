package httpapi_test

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/mirza76/todo-service/internal/httpapi"
	"github.com/mirza76/todo-service/internal/metrics"
	"github.com/mirza76/todo-service/internal/service"
	"github.com/mirza76/todo-service/internal/storage/memory"
)

func TestMetricsUseRoutePatterns(t *testing.T) {
	reg := metrics.NewRegistry("test")
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	h := httpapi.NewHandler(httpapi.Config{
		Service:        service.New(memory.New()),
		Logger:         slog.New(slog.DiscardHandler),
		Liveness:       ok,
		Readiness:      ok,
		MaxBodyBytes:   1024,
		RequestTimeout: time.Second,
		Metrics:        metrics.NewHTTP(reg),
	})

	created := createTodo(t, h, "measured")
	do(t, h, request{method: http.MethodGet, path: "/todos/" + created.ID})
	do(t, h, request{method: http.MethodGet, path: "/todos/" + uuid.NewString()}) // 404
	do(t, h, request{method: http.MethodGet, path: "/wp-admin/setup.php"})        // scanner noise
	do(t, h, request{method: http.MethodGet, path: "/.env"})

	expected := `
# HELP http_requests_total HTTP requests processed, by method, route pattern, and status code.
# TYPE http_requests_total counter
http_requests_total{code="200",method="get",route="/todos/{id}"} 1
http_requests_total{code="201",method="post",route="/todos"} 1
http_requests_total{code="404",method="get",route="/todos/{id}"} 1
http_requests_total{code="404",method="get",route="unmatched"} 2
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "http_requests_total"); err != nil {
		t.Error(err)
	}

	// No raw path or ID may ever appear as a label value.
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range families {
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if strings.Contains(l.GetValue(), created.ID) || strings.Contains(l.GetValue(), "wp-admin") {
					t.Errorf("%s has high-cardinality label %s=%q", mf.GetName(), l.GetName(), l.GetValue())
				}
			}
		}
	}
}
