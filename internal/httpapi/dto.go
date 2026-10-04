package httpapi

import (
	"time"

	"github.com/google/uuid"

	"github.com/mirza76/todo-service/internal/service"
	"github.com/mirza76/todo-service/internal/todo"
)

// Request and response types are separate from the domain model so that the
// public API contract can evolve independently of internal structures.

// Pointer fields distinguish "absent" from the zero value, which is how
// required fields are detected.
type createTodoRequest struct {
	Title     *string `json:"title"`
	Completed *bool   `json:"completed"`
}

func (req createTodoRequest) toInput() (service.CreateInput, error) {
	if req.Title == nil {
		return service.CreateInput{}, requiredFields("title")
	}
	in := service.CreateInput{Title: *req.Title}
	if req.Completed != nil {
		in.Completed = *req.Completed
	}
	return in, nil
}

// replaceTodoRequest requires every field: PUT replaces the whole resource.
type replaceTodoRequest struct {
	Title     *string `json:"title"`
	Completed *bool   `json:"completed"`
}

func (req replaceTodoRequest) toInput() (service.ReplaceInput, error) {
	var missing []string
	if req.Title == nil {
		missing = append(missing, "title")
	}
	if req.Completed == nil {
		missing = append(missing, "completed")
	}
	if len(missing) > 0 {
		return service.ReplaceInput{}, requiredFields(missing...)
	}
	return service.ReplaceInput{Title: *req.Title, Completed: *req.Completed}, nil
}

func requiredFields(names ...string) *todo.ValidationError {
	fields := make([]todo.FieldError, len(names))
	for i, n := range names {
		fields[i] = todo.FieldError{Field: n, Message: "is required"}
	}
	return &todo.ValidationError{Fields: fields}
}

type todoResponse struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	Completed bool      `json:"completed"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toTodoResponse(t todo.Todo) todoResponse {
	return todoResponse{
		ID:        t.ID,
		Title:     t.Title,
		Completed: t.Completed,
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
	}
}

type listTodosResponse struct {
	Items  []todoResponse `json:"items"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}
