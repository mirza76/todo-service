package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	legacyrouter "github.com/getkin/kin-openapi/routers/legacy"

	"github.com/mirza76/todo-service/api"
	"github.com/mirza76/todo-service/internal/health"
	"github.com/mirza76/todo-service/internal/httpapi"
	"github.com/mirza76/todo-service/internal/service"
	"github.com/mirza76/todo-service/internal/storage/memory"
)

// TestOpenAPIContract drives a realistic scenario through the real handler
// and validates every exchange against api/openapi.yaml, so the published
// contract and the implementation cannot drift apart.
func TestOpenAPIContract(t *testing.T) {
	doc := loadSpec(t)
	router, err := legacyrouter.NewRouter(doc)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}

	logger := slog.New(slog.DiscardHandler)
	checker := health.New(logger, time.Second)
	h := httpapi.NewHandler(httpapi.Config{
		Service:        service.New(memory.New()),
		Logger:         logger,
		Liveness:       checker.Live,
		Readiness:      checker.Ready,
		MaxBodyBytes:   1 << 20,
		RequestTimeout: time.Second,
	})

	var id string
	todoPath := func() string { return "/todos/" + id }

	steps := []struct {
		name        string
		method      string
		path        func() string
		body        string
		contentType string
		ifMatch     string
		wantStatus  int
		// invalidRequest marks deliberately invalid requests: only the
		// response is validated against the spec.
		invalidRequest bool
		after          func(body []byte)
	}{
		{
			name: "create", method: http.MethodPost, path: static("/todos"),
			body: `{"title":"Contract test"}`, wantStatus: http.StatusCreated,
			after: func(body []byte) {
				var created struct{ ID string }
				if err := json.Unmarshal(body, &created); err != nil {
					t.Fatalf("decode created todo: %v", err)
				}
				id = created.ID
			},
		},
		{name: "get", method: http.MethodGet, path: todoPath, wantStatus: http.StatusOK},
		{name: "list filtered and paginated", method: http.MethodGet, path: static("/todos?completed=false&limit=5&offset=0"), wantStatus: http.StatusOK},
		{name: "replace", method: http.MethodPut, path: todoPath, body: `{"title":"Replaced","completed":true}`, wantStatus: http.StatusOK},
		{name: "patch (merge-patch+json)", method: http.MethodPatch, path: todoPath, body: `{"completed":false}`, contentType: "application/merge-patch+json", wantStatus: http.StatusOK},
		{name: "patch (application/json)", method: http.MethodPatch, path: todoPath, body: `{"title":"Patched"}`, wantStatus: http.StatusOK},
		// Versions so far: create 1, replace 2, patch 3, patch 4.
		{name: "patch with current If-Match", method: http.MethodPatch, path: todoPath, body: `{"completed":true}`, ifMatch: `"4"`, wantStatus: http.StatusOK},
		{name: "replace with stale If-Match", method: http.MethodPut, path: todoPath, body: `{"title":"Late","completed":false}`, ifMatch: `"1"`, wantStatus: http.StatusPreconditionFailed},
		{name: "delete", method: http.MethodDelete, path: todoPath, wantStatus: http.StatusNoContent},
		{name: "get deleted", method: http.MethodGet, path: todoPath, wantStatus: http.StatusNotFound},

		{name: "create invalid title", method: http.MethodPost, path: static("/todos"), body: `{"title":""}`, wantStatus: http.StatusUnprocessableEntity, invalidRequest: true},
		{name: "create malformed JSON", method: http.MethodPost, path: static("/todos"), body: `{"title":`, wantStatus: http.StatusBadRequest, invalidRequest: true},
		{name: "create wrong content type", method: http.MethodPost, path: static("/todos"), body: `{"title":"x"}`, contentType: "text/plain", wantStatus: http.StatusUnsupportedMediaType, invalidRequest: true},
		{name: "list limit out of range", method: http.MethodGet, path: static("/todos?limit=0"), wantStatus: http.StatusUnprocessableEntity, invalidRequest: true},

		{name: "liveness", method: http.MethodGet, path: static("/livez"), wantStatus: http.StatusOK},
		{name: "readiness", method: http.MethodGet, path: static("/readyz"), wantStatus: http.StatusOK},
		{name: "openapi document", method: http.MethodGet, path: static("/openapi.yaml"), wantStatus: http.StatusOK},
	}

	exercised := map[string]bool{}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			newReq := func() *http.Request {
				var body io.Reader = http.NoBody
				if st.body != "" {
					body = bytes.NewBufferString(st.body)
				}
				r := httptest.NewRequestWithContext(t.Context(), st.method, st.path(), body)
				if st.body != "" {
					ct := st.contentType
					if ct == "" {
						ct = "application/json"
					}
					r.Header.Set("Content-Type", ct)
				}
				if st.ifMatch != "" {
					r.Header.Set("If-Match", st.ifMatch)
				}
				return r
			}

			// The validator consumes the body, so it gets its own copy.
			validationReq := newReq()
			route, pathParams, err := router.FindRoute(validationReq)
			if err != nil {
				t.Fatalf("operation not documented in spec: %s %s: %v", st.method, st.path(), err)
			}
			exercised[route.Operation.OperationID] = true

			reqInput := &openapi3filter.RequestValidationInput{
				Request:    validationReq,
				PathParams: pathParams,
				Route:      route,
			}
			if !st.invalidRequest {
				if err := openapi3filter.ValidateRequest(t.Context(), reqInput); err != nil {
					t.Errorf("request does not match spec: %v", err)
				}
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, newReq())
			if rec.Code != st.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, st.wantStatus, rec.Body)
			}

			respInput := &openapi3filter.ResponseValidationInput{
				RequestValidationInput: reqInput,
				Status:                 rec.Code,
				Header:                 rec.Header(),
				Body:                   io.NopCloser(bytes.NewReader(rec.Body.Bytes())),
				Options:                &openapi3filter.Options{IncludeResponseStatus: true},
			}
			if err := openapi3filter.ValidateResponse(t.Context(), respInput); err != nil {
				t.Errorf("response does not match spec: %v\nbody: %s", err, rec.Body)
			}

			if st.after != nil {
				st.after(rec.Body.Bytes())
			}
		})
	}

	// Every documented operation must be covered by the scenario above.
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			if !exercised[op.OperationID] {
				t.Errorf("operation %s (%s %s) is documented but not exercised", op.OperationID, method, path)
			}
		}
	}
}

func static(p string) func() string { return func() string { return p } }

func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData(api.Spec)
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	if err := doc.Validate(t.Context()); err != nil {
		t.Fatalf("spec is not a valid OpenAPI document: %v", err)
	}
	// Match routes by path only, regardless of the test request's host.
	doc.Servers = nil
	return doc
}

func init() {
	// Problem Details and merge-patch are JSON; teach the validator so.
	openapi3filter.RegisterBodyDecoder("application/problem+json", openapi3filter.JSONBodyDecoder)
	openapi3filter.RegisterBodyDecoder("application/merge-patch+json", openapi3filter.JSONBodyDecoder)
}
