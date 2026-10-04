package httpapi

import (
	"bytes"
	"encoding/json"
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

// patchTodoRequest follows JSON Merge Patch (RFC 7396): absent fields are
// left unchanged, and null would mean "remove the field". Neither field can
// be removed, so explicit nulls are rejected.
type patchTodoRequest struct {
	Title     *string
	Completed *bool
	nulls     map[string]bool // fields explicitly set to null
	// isObject is set only when the body was a JSON object. A bare `null`
	// body decodes without error in encoding/json, but as a merge patch it
	// would mean "replace the resource with null", which is invalid.
	isObject bool
}

// UnmarshalJSON decodes in two passes. Pointers alone can't tell an absent
// field from an explicit null, so:
//  1. a typed, strict pass gives precise errors (field type mismatches,
//     unknown fields) exactly like the other endpoints;
//  2. a raw pass records which fields were explicitly null.
func (req *patchTodoRequest) UnmarshalJSON(b []byte) error {
	var typed struct {
		Title     *string `json:"title"`
		Completed *bool   `json:"completed"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&typed); err != nil {
		return err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if raw == nil { // the body was `null`
		return nil
	}
	req.isObject = true
	req.Title, req.Completed = typed.Title, typed.Completed
	req.nulls = make(map[string]bool)
	for field, value := range raw {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			req.nulls[field] = true
		}
	}
	return nil
}

func (req patchTodoRequest) toPatch() (todo.Patch, error) {
	if !req.isObject {
		return todo.Patch{}, badRequest("Request body must be a JSON object.")
	}
	var nulls []todo.FieldError
	for _, field := range []string{"title", "completed"} { // fixed order
		if req.nulls[field] {
			nulls = append(nulls, todo.FieldError{Field: field, Message: "must not be null"})
		}
	}
	if len(nulls) > 0 {
		return todo.Patch{}, &todo.ValidationError{Fields: nulls}
	}
	return todo.Patch{Title: req.Title, Completed: req.Completed}, nil
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
