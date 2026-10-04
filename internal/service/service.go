// Package service implements the todo use cases. It orchestrates the domain
// and the Repository port, and owns cross-cutting decisions such as ID
// generation, timestamps, and pagination bounds. It depends only on
// abstractions, so it is unaware of HTTP or the concrete storage engine.
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/mirza76/todo-service/internal/todo"
)

// Pagination bounds for List.
const (
	DefaultListLimit = 20
	MaxListLimit     = 100
)

// CreateInput is the data needed to create a todo.
type CreateInput struct {
	Title     string
	Completed bool
}

// ReplaceInput is the full set of client-controlled fields (PUT semantics).
type ReplaceInput struct {
	Title     string
	Completed bool
}

// Service provides the todo use cases.
type Service struct {
	repo  todo.Repository
	now   func() time.Time
	newID func() (uuid.UUID, error)
}

// Option customizes a Service; used mainly to make tests deterministic.
type Option func(*Service)

// WithClock overrides the time source.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithIDGenerator overrides ID generation.
func WithIDGenerator(newID func() (uuid.UUID, error)) Option {
	return func(s *Service) { s.newID = newID }
}

// New returns a Service backed by repo. By default IDs are UUIDv7, which are
// time-ordered and therefore index-friendly in B-tree databases.
func New(repo todo.Repository, opts ...Option) *Service {
	s := &Service{repo: repo, now: time.Now, newID: uuid.NewV7}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Create validates and stores a new todo.
func (s *Service) Create(ctx context.Context, in CreateInput) (todo.Todo, error) {
	id, err := s.newID()
	if err != nil {
		return todo.Todo{}, fmt.Errorf("generate id: %w", err)
	}
	t, err := todo.New(id, in.Title, in.Completed, s.timestamp())
	if err != nil {
		return todo.Todo{}, err
	}
	if err := s.repo.Create(ctx, t); err != nil {
		return todo.Todo{}, fmt.Errorf("create todo: %w", err)
	}
	return t, nil
}

// Get returns a single todo.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (todo.Todo, error) {
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return todo.Todo{}, fmt.Errorf("get todo %s: %w", id, err)
	}
	return t, nil
}

// List returns one page of todos. Limit must be within [1, MaxListLimit] and
// Offset must not be negative.
func (s *Service) List(ctx context.Context, params todo.ListParams) (todo.Page, error) {
	if err := validateListParams(params); err != nil {
		return todo.Page{}, err
	}
	page, err := s.repo.List(ctx, params)
	if err != nil {
		return todo.Page{}, fmt.Errorf("list todos: %w", err)
	}
	return page, nil
}

// Replace overwrites all client-controlled fields of an existing todo.
//
// Concurrent replacements of the same todo are last-write-wins.
func (s *Service) Replace(ctx context.Context, id uuid.UUID, in ReplaceInput) (todo.Todo, error) {
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return todo.Todo{}, fmt.Errorf("get todo %s: %w", id, err)
	}
	if err := t.Replace(in.Title, in.Completed, s.timestamp()); err != nil {
		return todo.Todo{}, err
	}
	if err := s.repo.Update(ctx, t); err != nil {
		return todo.Todo{}, fmt.Errorf("update todo %s: %w", id, err)
	}
	return t, nil
}

// Update applies a partial update (PATCH semantics). An empty patch is a
// no-op that returns the current todo without bumping UpdatedAt.
//
// Concurrent updates of the same todo are last-write-wins.
func (s *Service) Update(ctx context.Context, id uuid.UUID, patch todo.Patch) (todo.Todo, error) {
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return todo.Todo{}, fmt.Errorf("get todo %s: %w", id, err)
	}
	if patch.IsEmpty() {
		return t, nil
	}
	if err := t.Apply(patch, s.timestamp()); err != nil {
		return todo.Todo{}, err
	}
	if err := s.repo.Update(ctx, t); err != nil {
		return todo.Todo{}, fmt.Errorf("update todo %s: %w", id, err)
	}
	return t, nil
}

// Delete removes a todo.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete todo %s: %w", id, err)
	}
	return nil
}

// timestamp returns the current time in UTC, truncated to microseconds so
// that values round-trip through PostgreSQL (microsecond precision) unchanged.
func (s *Service) timestamp() time.Time {
	return s.now().UTC().Truncate(time.Microsecond)
}

func validateListParams(p todo.ListParams) error {
	var fields []todo.FieldError
	if p.Limit < 1 || p.Limit > MaxListLimit {
		fields = append(fields, todo.FieldError{
			Field:   "limit",
			Message: fmt.Sprintf("must be between 1 and %d", MaxListLimit),
		})
	}
	if p.Offset < 0 {
		fields = append(fields, todo.FieldError{Field: "offset", Message: "must not be negative"})
	}
	if len(fields) > 0 {
		return &todo.ValidationError{Fields: fields}
	}
	return nil
}
