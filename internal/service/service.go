// Package service implements the todo use cases. It orchestrates the domain
// and the Repository port, and owns cross-cutting decisions such as ID
// generation, timestamps, and pagination bounds. It depends only on
// abstractions, so it is unaware of HTTP or the concrete storage engine.
package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

// IfMatch is a client precondition on the current version (HTTP If-Match).
// The zero value is unconditional.
type IfMatch struct {
	set      bool
	versions []int64
}

// MatchVersions returns a precondition satisfied only by one of versions.
// With no versions it can never be satisfied (e.g. only weak or malformed
// ETags were sent).
func MatchVersions(versions ...int64) IfMatch {
	return IfMatch{set: true, versions: versions}
}

func (m IfMatch) matches(version int64) bool {
	return !m.set || slices.Contains(m.versions, version)
}

// maxWriteAttempts bounds retries of an unconditional read-modify-write that
// keeps losing compare-and-set races.
const maxWriteAttempts = 3

// Replace overwrites all client-controlled fields of an existing todo.
func (s *Service) Replace(ctx context.Context, id uuid.UUID, in ReplaceInput, cond IfMatch) (todo.Todo, error) {
	return s.modify(ctx, id, cond, func(t *todo.Todo) (bool, error) {
		return true, t.Replace(in.Title, in.Completed, s.timestamp())
	})
}

// Update applies a partial update (PATCH semantics). An empty patch is a
// no-op that returns the current todo without bumping its version.
func (s *Service) Update(ctx context.Context, id uuid.UUID, patch todo.Patch, cond IfMatch) (todo.Todo, error) {
	return s.modify(ctx, id, cond, func(t *todo.Todo) (bool, error) {
		if patch.IsEmpty() {
			return false, nil
		}
		return true, t.Apply(patch, s.timestamp())
	})
}

// modify runs a read-modify-write with optimistic concurrency control:
//
//   - The client precondition (If-Match) is checked against the version read;
//     a mismatch is ErrPreconditionFailed.
//   - The write is a compare-and-set on that version, so a concurrent change
//     made between the read and the write (on any replica) is detected.
//   - With a precondition, such a race means the client's version is now
//     stale: ErrPreconditionFailed. Without one, the change is re-applied to
//     the fresh state, so concurrent PATCHes of different fields both
//     survive. After maxWriteAttempts it gives up with ErrVersionConflict.
func (s *Service) modify(ctx context.Context, id uuid.UUID, cond IfMatch, change func(*todo.Todo) (bool, error)) (todo.Todo, error) {
	for attempt := 1; ; attempt++ {
		t, err := s.repo.Get(ctx, id)
		if err != nil {
			return todo.Todo{}, fmt.Errorf("get todo %s: %w", id, err)
		}
		if !cond.matches(t.Version) {
			return todo.Todo{}, fmt.Errorf("todo %s is at version %d: %w", id, t.Version, todo.ErrPreconditionFailed)
		}

		readVersion := t.Version
		changed, err := change(&t)
		if err != nil {
			return todo.Todo{}, err
		}
		if !changed {
			return t, nil
		}

		err = s.repo.Update(ctx, t, readVersion)
		switch {
		case err == nil:
			return t, nil
		case errors.Is(err, todo.ErrVersionConflict) && cond.set:
			return todo.Todo{}, fmt.Errorf("update todo %s: %w", id, todo.ErrPreconditionFailed)
		case errors.Is(err, todo.ErrVersionConflict) && attempt < maxWriteAttempts:
			continue
		default:
			return todo.Todo{}, fmt.Errorf("update todo %s: %w", id, err)
		}
	}
}

// Delete removes a todo, honoring an optional If-Match precondition.
func (s *Service) Delete(ctx context.Context, id uuid.UUID, cond IfMatch) error {
	expected := todo.AnyVersion
	if cond.set {
		t, err := s.repo.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("get todo %s: %w", id, err)
		}
		if !cond.matches(t.Version) {
			return fmt.Errorf("todo %s is at version %d: %w", id, t.Version, todo.ErrPreconditionFailed)
		}
		expected = t.Version
	}

	err := s.repo.Delete(ctx, id, expected)
	if errors.Is(err, todo.ErrVersionConflict) {
		err = todo.ErrPreconditionFailed // changed between our read and delete
	}
	if err != nil {
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
