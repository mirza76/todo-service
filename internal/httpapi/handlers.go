package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"github.com/mirza76/todo-service/internal/service"
	"github.com/mirza76/todo-service/internal/todo"
)

// TodoService is the behavior the HTTP layer needs. It is defined here, by
// the consumer, so handlers can be tested with a stub (e.g. to force a 500).
type TodoService interface {
	Create(ctx context.Context, in service.CreateInput) (todo.Todo, error)
	Get(ctx context.Context, id uuid.UUID) (todo.Todo, error)
	List(ctx context.Context, params todo.ListParams) (todo.Page, error)
	Replace(ctx context.Context, id uuid.UUID, in service.ReplaceInput) (todo.Todo, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type todoHandler struct {
	svc          TodoService
	logger       *slog.Logger
	maxBodyBytes int64
}

func (h *todoHandler) list(w http.ResponseWriter, r *http.Request) {
	params, err := parseListParams(r.URL.Query())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	page, err := h.svc.List(r.Context(), params)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	items := make([]todoResponse, len(page.Items))
	for i, t := range page.Items {
		items[i] = toTodoResponse(t)
	}
	writeJSON(w, http.StatusOK, listTodosResponse{
		Items:  items,
		Total:  page.Total,
		Limit:  params.Limit,
		Offset: params.Offset,
	})
}

func (h *todoHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createTodoRequest
	if err := decodeJSON(w, r, &req, h.maxBodyBytes); err != nil {
		h.fail(w, r, err)
		return
	}
	in, err := req.toInput()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	t, err := h.svc.Create(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Location", "/todos/"+t.ID.String())
	writeJSON(w, http.StatusCreated, toTodoResponse(t))
}

func (h *todoHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	t, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTodoResponse(t))
}

func (h *todoHandler) replace(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var req replaceTodoRequest
	if err := decodeJSON(w, r, &req, h.maxBodyBytes); err != nil {
		h.fail(w, r, err)
		return
	}
	in, err := req.toInput()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	t, err := h.svc.Replace(r.Context(), id, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTodoResponse(t))
}

func (h *todoHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *todoHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	writeError(w, r, h.logger, err)
}

// pathID parses the {id} path segment. A malformed ID cannot identify an
// existing resource, so it is reported as 404 rather than 400.
func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse id %q: %w", r.PathValue("id"), todo.ErrNotFound)
	}
	return id, nil
}

// parseListParams reads limit and offset. Non-integer values are malformed
// requests (400); range checks belong to the service layer (422).
func parseListParams(q url.Values) (todo.ListParams, error) {
	params := todo.ListParams{Limit: service.DefaultListLimit}
	for _, p := range []struct {
		name string
		dst  *int
	}{
		{"limit", &params.Limit},
		{"offset", &params.Offset},
	} {
		if !q.Has(p.name) {
			continue
		}
		n, err := strconv.Atoi(q.Get(p.name))
		if err != nil {
			return todo.ListParams{}, badRequest(fmt.Sprintf("Query parameter %q must be an integer.", p.name))
		}
		*p.dst = n
	}
	return params, nil
}
