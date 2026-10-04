package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mirza76/todo-service/internal/httpapi"
	"github.com/mirza76/todo-service/internal/service"
	"github.com/mirza76/todo-service/internal/storage/memory"
	"github.com/mirza76/todo-service/internal/todo"
)

const maxBodyBytes = 1024

// --- test fixtures -------------------------------------------------------

type todoJSON struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Completed bool      `json:"completed"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type listJSON struct {
	Items  []todoJSON `json:"items"`
	Total  int        `json:"total"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
}

type problemJSON struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
	Errors   []struct {
		Field   string `json:"field"`
		Message string `json:"message"`
	} `json:"errors"`
}

func newHandler(svc httpapi.TodoService) http.Handler {
	return newHandlerWithTimeout(svc, time.Second)
}

func newHandlerWithTimeout(svc httpapi.TodoService, timeout time.Duration) http.Handler {
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	return httpapi.NewHandler(httpapi.Config{
		Service:        svc,
		Logger:         slog.New(slog.DiscardHandler),
		Liveness:       ok,
		Readiness:      ok,
		MaxBodyBytes:   maxBodyBytes,
		RequestTimeout: timeout,
	})
}

func newRealHandler() http.Handler {
	return newHandler(service.New(memory.New()))
}

type request struct {
	method      string
	path        string
	body        string
	contentType string // defaults to application/json when body is set
}

func do(t *testing.T, h http.Handler, req request) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader = http.NoBody
	if req.body != "" {
		body = strings.NewReader(req.body)
	}
	r := httptest.NewRequestWithContext(t.Context(), req.method, req.path, body)
	switch {
	case req.contentType != "":
		r.Header.Set("Content-Type", req.contentType)
	case req.body != "":
		r.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return v
}

func createTodo(t *testing.T, h http.Handler, title string) todoJSON {
	t.Helper()
	rec := do(t, h, request{method: http.MethodPost, path: "/todos", body: fmt.Sprintf(`{"title":%q}`, title)})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %q: status %d, body %s", title, rec.Code, rec.Body)
	}
	return decode[todoJSON](t, rec)
}

func assertProblem(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantDetail string) problemJSON {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, wantStatus, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	p := decode[problemJSON](t, rec)
	if p.Type != "about:blank" || p.Status != wantStatus || p.Title != http.StatusText(wantStatus) {
		t.Errorf("problem = %+v, want type about:blank, status %d, title %q", p, wantStatus, http.StatusText(wantStatus))
	}
	if wantDetail != "" && !strings.Contains(p.Detail, wantDetail) {
		t.Errorf("detail = %q, want it to contain %q", p.Detail, wantDetail)
	}
	return p
}

func assertFieldErrors(t *testing.T, p problemJSON, want map[string]string) {
	t.Helper()
	got := make(map[string]string, len(p.Errors))
	for _, e := range p.Errors {
		got[e.Field] = e.Message
	}
	if len(got) != len(want) {
		t.Fatalf("field errors = %v, want %v", got, want)
	}
	for field, msg := range want {
		if got[field] != msg {
			t.Errorf("field %q error = %q, want %q", field, got[field], msg)
		}
	}
}

// --- happy paths ---------------------------------------------------------

func TestCreate(t *testing.T) {
	h := newRealHandler()

	rec := do(t, h, request{method: http.MethodPost, path: "/todos", body: `{"title":"  Buy milk ","completed":true}`})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	got := decode[todoJSON](t, rec)
	if _, err := uuid.Parse(got.ID); err != nil {
		t.Errorf("id %q is not a UUID", got.ID)
	}
	if got.Title != "Buy milk" || !got.Completed {
		t.Errorf("got %+v, want title %q completed true", got, "Buy milk")
	}
	if got.CreatedAt.IsZero() || !got.CreatedAt.Equal(got.UpdatedAt) {
		t.Errorf("timestamps created_at=%v updated_at=%v, want equal and non-zero", got.CreatedAt, got.UpdatedAt)
	}
	if loc := rec.Header().Get("Location"); loc != "/todos/"+got.ID {
		t.Errorf("Location = %q, want /todos/%s", loc, got.ID)
	}
}

func TestCreate_CompletedDefaultsToFalse(t *testing.T) {
	got := createTodo(t, newRealHandler(), "Walk dog")
	if got.Completed {
		t.Error("completed = true, want default false")
	}
}

func TestGet(t *testing.T) {
	h := newRealHandler()
	created := createTodo(t, h, "Read book")

	rec := do(t, h, request{method: http.MethodGet, path: "/todos/" + created.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	if got := decode[todoJSON](t, rec); got != created {
		t.Errorf("got %+v, want %+v", got, created)
	}
}

func TestReplace(t *testing.T) {
	h := newRealHandler()
	created := createTodo(t, h, "Draft")

	rec := do(t, h, request{method: http.MethodPut, path: "/todos/" + created.ID, body: `{"title":"Final","completed":true}`})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	got := decode[todoJSON](t, rec)
	if got.ID != created.ID || got.Title != "Final" || !got.Completed || !got.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("got %+v, want id %s title Final completed true created_at unchanged", got, created.ID)
	}

	// The change is persisted.
	stored := decode[todoJSON](t, do(t, h, request{method: http.MethodGet, path: "/todos/" + created.ID}))
	if stored != got {
		t.Errorf("stored %+v, want %+v", stored, got)
	}
}

func TestPatch(t *testing.T) {
	h := newRealHandler()
	created := createTodo(t, h, "Draft")
	path := "/todos/" + created.ID

	t.Run("completed only keeps title", func(t *testing.T) {
		rec := do(t, h, request{method: http.MethodPatch, path: path, body: `{"completed":true}`})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
		}
		got := decode[todoJSON](t, rec)
		if got.Title != "Draft" || !got.Completed || !got.CreatedAt.Equal(created.CreatedAt) {
			t.Errorf("got %+v, want title Draft completed true created_at unchanged", got)
		}
	})

	t.Run("title only keeps completed, merge-patch content type", func(t *testing.T) {
		rec := do(t, h, request{method: http.MethodPatch, path: path, body: `{"title":" Final "}`, contentType: "application/merge-patch+json"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
		}
		got := decode[todoJSON](t, rec)
		if got.Title != "Final" || !got.Completed {
			t.Errorf("got %+v, want title Final completed true", got)
		}
	})

	t.Run("empty patch returns todo unchanged", func(t *testing.T) {
		before := decode[todoJSON](t, do(t, h, request{method: http.MethodGet, path: path}))
		rec := do(t, h, request{method: http.MethodPatch, path: path, body: `{}`})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
		}
		if got := decode[todoJSON](t, rec); got != before {
			t.Errorf("got %+v, want unchanged %+v", got, before)
		}
	})

	t.Run("errors", func(t *testing.T) {
		tests := []struct {
			name        string
			path        string
			body        string
			contentType string
			wantStatus  int
			wantDetail  string
			wantFields  map[string]string
		}{
			{"null title", path, `{"title":null}`, "", http.StatusUnprocessableEntity, "", map[string]string{"title": "must not be null"}},
			{"null both", path, `{"title":null,"completed":null}`, "", http.StatusUnprocessableEntity, "", map[string]string{"title": "must not be null", "completed": "must not be null"}},
			{"blank title", path, `{"title":"  "}`, "", http.StatusUnprocessableEntity, "", map[string]string{"title": "must not be empty"}},
			{"wrong type", path, `{"completed":"yes"}`, "", http.StatusBadRequest, `Field "completed" must be a boolean`, nil},
			{"unknown field", path, `{"done":true}`, "", http.StatusBadRequest, `unknown field "done"`, nil},
			{"null body", path, `null`, "", http.StatusBadRequest, "must be a JSON object", nil},
			{"array body", path, `[{"completed":true}]`, "", http.StatusBadRequest, "must be a JSON object", nil},
			{"unsupported content type", path, `{"completed":true}`, "text/plain", http.StatusUnsupportedMediaType, "application/json or application/merge-patch+json", nil},
			{"unknown id", "/todos/" + uuid.NewString(), `{"completed":true}`, "", http.StatusNotFound, "", nil},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				rec := do(t, h, request{method: http.MethodPatch, path: tt.path, body: tt.body, contentType: tt.contentType})
				p := assertProblem(t, rec, tt.wantStatus, tt.wantDetail)
				if tt.wantFields != nil {
					assertFieldErrors(t, p, tt.wantFields)
				}
			})
		}
	})
}

func TestList_FilterCompleted(t *testing.T) {
	h := newRealHandler()
	open := createTodo(t, h, "open")
	done := createTodo(t, h, "done")
	if rec := do(t, h, request{method: http.MethodPatch, path: "/todos/" + done.ID, body: `{"completed":true}`}); rec.Code != http.StatusOK {
		t.Fatalf("patch: status %d", rec.Code)
	}

	tests := []struct {
		query  string
		wantID string
	}{
		{"completed=true", done.ID},
		{"completed=false", open.ID},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := decode[listJSON](t, do(t, h, request{method: http.MethodGet, path: "/todos?" + tt.query}))
			if got.Total != 1 || len(got.Items) != 1 || got.Items[0].ID != tt.wantID {
				t.Errorf("got total %d items %+v, want only %s", got.Total, got.Items, tt.wantID)
			}
		})
	}

	for _, bad := range []string{"completed=yes", "completed=1", "completed=TRUE", "completed="} {
		t.Run("rejects "+bad, func(t *testing.T) {
			assertProblem(t, do(t, h, request{method: http.MethodGet, path: "/todos?" + bad}), http.StatusBadRequest, `"completed" must be true or false`)
		})
	}
}

func TestDelete(t *testing.T) {
	h := newRealHandler()
	created := createTodo(t, h, "Temp")

	rec := do(t, h, request{method: http.MethodDelete, path: "/todos/" + created.ID})
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("status = %d body %q, want 204 with empty body", rec.Code, rec.Body)
	}

	assertProblem(t, do(t, h, request{method: http.MethodGet, path: "/todos/" + created.ID}), http.StatusNotFound, "")
	assertProblem(t, do(t, h, request{method: http.MethodDelete, path: "/todos/" + created.ID}), http.StatusNotFound, "")
}

func TestList(t *testing.T) {
	h := newRealHandler()

	t.Run("empty list encodes items as [] not null", func(t *testing.T) {
		rec := do(t, h, request{method: http.MethodGet, path: "/todos"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"items":[]`) {
			t.Errorf("body %s does not contain \"items\":[]", rec.Body)
		}
		got := decode[listJSON](t, rec)
		if got.Total != 0 || got.Limit != service.DefaultListLimit || got.Offset != 0 {
			t.Errorf("got %+v, want total 0 limit %d offset 0", got, service.DefaultListLimit)
		}
	})

	var created []todoJSON
	for i := range 5 {
		created = append(created, createTodo(t, h, fmt.Sprintf("item %d", i)))
	}

	t.Run("default page returns all in creation order", func(t *testing.T) {
		got := decode[listJSON](t, do(t, h, request{method: http.MethodGet, path: "/todos"}))
		if got.Total != 5 || len(got.Items) != 5 {
			t.Fatalf("total %d items %d, want 5 and 5", got.Total, len(got.Items))
		}
		for i := range created {
			if got.Items[i].ID != created[i].ID {
				t.Errorf("items[%d] = %s, want %s", i, got.Items[i].ID, created[i].ID)
			}
		}
	})

	t.Run("limit and offset", func(t *testing.T) {
		got := decode[listJSON](t, do(t, h, request{method: http.MethodGet, path: "/todos?limit=2&offset=3"}))
		if got.Total != 5 || got.Limit != 2 || got.Offset != 3 || len(got.Items) != 2 {
			t.Fatalf("got total %d limit %d offset %d items %d, want 5 2 3 2", got.Total, got.Limit, got.Offset, len(got.Items))
		}
		if got.Items[0].ID != created[3].ID || got.Items[1].ID != created[4].ID {
			t.Errorf("items = [%s %s], want [%s %s]", got.Items[0].ID, got.Items[1].ID, created[3].ID, created[4].ID)
		}
	})
}

// --- client errors -------------------------------------------------------

func TestList_InvalidQuery(t *testing.T) {
	h := newRealHandler()

	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantDetail string
		wantFields map[string]string
	}{
		{"non-integer limit", "limit=abc", http.StatusBadRequest, `"limit" must be an integer`, nil},
		{"non-integer offset", "offset=1.5", http.StatusBadRequest, `"offset" must be an integer`, nil},
		{"zero limit", "limit=0", http.StatusUnprocessableEntity, "", map[string]string{"limit": "must be between 1 and 100"}},
		{"limit above max", "limit=101", http.StatusUnprocessableEntity, "", map[string]string{"limit": "must be between 1 and 100"}},
		{"negative offset", "offset=-1", http.StatusUnprocessableEntity, "", map[string]string{"offset": "must not be negative"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := assertProblem(t, do(t, h, request{method: http.MethodGet, path: "/todos?" + tt.query}), tt.wantStatus, tt.wantDetail)
			if tt.wantFields != nil {
				assertFieldErrors(t, p, tt.wantFields)
			}
		})
	}
}

func TestRequestBodyErrors(t *testing.T) {
	h := newRealHandler()
	existing := createTodo(t, h, "Existing")

	tests := []struct {
		name        string
		body        string
		contentType string
		wantStatus  int
		wantDetail  string
	}{
		{"missing content type", `{"title":"x"}`, "text/plain", http.StatusUnsupportedMediaType, "Content-Type must be application/json"},
		{"content type with charset is accepted", `{"title":""}`, "application/json; charset=utf-8", http.StatusUnprocessableEntity, ""},
		{"empty body", "", "application/json", http.StatusBadRequest, "must not be empty"},
		{"malformed json", `{"title":`, "", http.StatusBadRequest, "malformed JSON"},
		{"invalid json syntax", `{title:"x"}`, "", http.StatusBadRequest, "malformed JSON"},
		{"wrong field type", `{"title":123}`, "", http.StatusBadRequest, `Field "title" must be a string`},
		{"wrong bool type", `{"title":"x","completed":"yes"}`, "", http.StatusBadRequest, `Field "completed" must be a boolean`},
		{"array instead of object", `[{"title":"x"}]`, "", http.StatusBadRequest, "must be a JSON object"},
		{"unknown field", `{"title":"x","priority":1}`, "", http.StatusBadRequest, `unknown field "priority"`},
		{"trailing data", `{"title":"x"}{"title":"y"}`, "", http.StatusBadRequest, "single JSON object"},
		{"body too large", `{"title":"` + strings.Repeat("a", maxBodyBytes) + `"}`, "", http.StatusRequestEntityTooLarge, "must not exceed 1024 bytes"},
	}

	targets := []struct{ method, path string }{
		{http.MethodPost, "/todos"},
		{http.MethodPut, "/todos/" + existing.ID},
	}

	for _, target := range targets {
		for _, tt := range tests {
			t.Run(target.method+"/"+tt.name, func(t *testing.T) {
				ct := tt.contentType
				if ct == "" {
					ct = "application/json"
				}
				rec := do(t, h, request{method: target.method, path: target.path, body: tt.body, contentType: ct})
				assertProblem(t, rec, tt.wantStatus, tt.wantDetail)
			})
		}
	}
}

func TestValidationErrors(t *testing.T) {
	h := newRealHandler()
	existing := createTodo(t, h, "Existing")
	put := "/todos/" + existing.ID

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantFields map[string]string
	}{
		{"create without title", http.MethodPost, "/todos", `{"completed":true}`, map[string]string{"title": "is required"}},
		{"create with null title", http.MethodPost, "/todos", `{"title":null}`, map[string]string{"title": "is required"}},
		{"create with blank title", http.MethodPost, "/todos", `{"title":"   "}`, map[string]string{"title": "must not be empty"}},
		{"create with long title", http.MethodPost, "/todos", fmt.Sprintf(`{"title":%q}`, strings.Repeat("a", 201)), map[string]string{"title": "must be at most 200 characters"}},
		{"replace with empty object", http.MethodPut, put, `{}`, map[string]string{"title": "is required", "completed": "is required"}},
		{"replace without completed", http.MethodPut, put, `{"title":"x"}`, map[string]string{"completed": "is required"}},
		{"replace with blank title", http.MethodPut, put, `{"title":"","completed":false}`, map[string]string{"title": "must not be empty"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := assertProblem(t, do(t, h, request{method: tt.method, path: tt.path, body: tt.body}), http.StatusUnprocessableEntity, "invalid fields")
			assertFieldErrors(t, p, tt.wantFields)
		})
	}

	// A failed replacement must not modify the stored todo.
	stored := decode[todoJSON](t, do(t, h, request{method: http.MethodGet, path: put}))
	if stored != existing {
		t.Errorf("stored %+v, want unchanged %+v", stored, existing)
	}
}

func TestNotFound(t *testing.T) {
	h := newRealHandler()
	missing := "/todos/" + uuid.NewString()

	tests := []struct {
		name string
		req  request
	}{
		{"get unknown id", request{method: http.MethodGet, path: missing}},
		{"replace unknown id", request{method: http.MethodPut, path: missing, body: `{"title":"x","completed":false}`}},
		{"delete unknown id", request{method: http.MethodDelete, path: missing}},
		{"malformed id", request{method: http.MethodGet, path: "/todos/not-a-uuid"}},
		{"unknown route", request{method: http.MethodGet, path: "/nope"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := assertProblem(t, do(t, h, tt.req), http.StatusNotFound, "")
			if p.Instance != tt.req.path {
				t.Errorf("instance = %q, want %q", p.Instance, tt.req.path)
			}
		})
	}
}

func TestMethodNotAllowed(t *testing.T) {
	rec := do(t, newRealHandler(), request{method: http.MethodPatch, path: "/todos"})
	assertProblem(t, rec, http.StatusMethodNotAllowed, "method is not allowed")
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "GET") || !strings.Contains(allow, "POST") {
		t.Errorf("Allow = %q, want it to list GET and POST", allow)
	}
}

// --- server-side failures ------------------------------------------------

// stubService lets tests force failures the real service cannot produce.
type stubService struct {
	httpapi.TodoService // nil; unused methods panic if called
	list                func(ctx context.Context) (todo.Page, error)
}

func (s stubService) List(ctx context.Context, _ todo.ListParams) (todo.Page, error) {
	return s.list(ctx)
}

func TestInternalErrorIsNotLeaked(t *testing.T) {
	h := newHandler(stubService{list: func(context.Context) (todo.Page, error) {
		return todo.Page{}, errors.New("pq: password authentication failed for user admin")
	}})

	rec := do(t, h, request{method: http.MethodGet, path: "/todos"})
	assertProblem(t, rec, http.StatusInternalServerError, "unexpected error")
	if strings.Contains(rec.Body.String(), "password") {
		t.Errorf("internal error leaked to client: %s", rec.Body)
	}
}

func TestPanicIsRecovered(t *testing.T) {
	h := newHandler(stubService{list: func(context.Context) (todo.Page, error) {
		panic("something went badly wrong")
	}})

	assertProblem(t, do(t, h, request{method: http.MethodGet, path: "/todos"}), http.StatusInternalServerError, "unexpected error")
}

func TestRequestTimeout(t *testing.T) {
	slow := stubService{list: func(ctx context.Context) (todo.Page, error) {
		<-ctx.Done() // a slow dependency that honors cancellation
		return todo.Page{}, ctx.Err()
	}}
	h := newHandlerWithTimeout(slow, 20*time.Millisecond)

	assertProblem(t, do(t, h, request{method: http.MethodGet, path: "/todos"}), http.StatusServiceUnavailable, "timed out")
}
