package todo

import (
	"errors"
	"strings"
)

// Sentinel errors returned by the domain and by every Repository
// implementation. Callers match them with errors.Is.
var (
	ErrNotFound      = errors.New("todo not found")
	ErrAlreadyExists = errors.New("todo already exists")
)

// FieldError describes why a single input field is invalid.
type FieldError struct {
	Field   string
	Message string
}

// ValidationError reports one or more invalid input fields. Callers match it
// with errors.As.
type ValidationError struct {
	Fields []FieldError
}

func titleError(message string) *ValidationError {
	return &ValidationError{Fields: []FieldError{{Field: "title", Message: message}}}
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		parts[i] = f.Field + " " + f.Message
	}
	return "validation failed: " + strings.Join(parts, "; ")
}
