// Package memory provides an in-memory, concurrency-safe todo.Repository.
// Data is lost on restart and is not shared between replicas, so it is
// intended for local development and tests; production uses Postgres.
package memory

import (
	"bytes"
	"cmp"
	"context"
	"slices"
	"sync"

	"github.com/google/uuid"

	"github.com/mirza76/todo-service/internal/todo"
)

// Repository stores todos in a map guarded by a RWMutex. Todo is a value type
// with no reference fields, so storing and returning copies prevents callers
// from mutating stored state.
type Repository struct {
	mu    sync.RWMutex
	items map[uuid.UUID]todo.Todo
}

// Compile-time check that Repository satisfies the port.
var _ todo.Repository = (*Repository)(nil)

// New returns an empty Repository.
func New() *Repository {
	return &Repository{items: make(map[uuid.UUID]todo.Todo)}
}

// Create stores t, failing if its ID already exists.
func (r *Repository) Create(ctx context.Context, t todo.Todo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.items[t.ID]; ok {
		return todo.ErrAlreadyExists
	}
	r.items[t.ID] = t
	return nil
}

// Get returns the todo with the given ID.
func (r *Repository) Get(ctx context.Context, id uuid.UUID) (todo.Todo, error) {
	if err := ctx.Err(); err != nil {
		return todo.Todo{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	t, ok := r.items[id]
	if !ok {
		return todo.Todo{}, todo.ErrNotFound
	}
	return t, nil
}

// List returns one page of todos ordered by CreatedAt, then ID. Sorting on
// every call is O(n log n); acceptable for a development store.
func (r *Repository) List(ctx context.Context, params todo.ListParams) (todo.Page, error) {
	if err := ctx.Err(); err != nil {
		return todo.Page{}, err
	}
	r.mu.RLock()
	all := make([]todo.Todo, 0, len(r.items))
	for _, t := range r.items {
		if params.Completed == nil || t.Completed == *params.Completed {
			all = append(all, t)
		}
	}
	r.mu.RUnlock()

	slices.SortFunc(all, func(a, b todo.Todo) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), bytes.Compare(a.ID[:], b.ID[:]))
	})

	start := min(params.Offset, len(all))
	end := min(start+params.Limit, len(all))
	return todo.Page{Items: all[start:end], Total: len(all)}, nil
}

// Update overwrites Title, Completed, and UpdatedAt of an existing todo.
func (r *Repository) Update(ctx context.Context, t todo.Todo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, ok := r.items[t.ID]
	if !ok {
		return todo.ErrNotFound
	}
	existing.Title = t.Title
	existing.Completed = t.Completed
	existing.UpdatedAt = t.UpdatedAt
	r.items[t.ID] = existing
	return nil
}

// Delete removes the todo with the given ID.
func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.items[id]; !ok {
		return todo.ErrNotFound
	}
	delete(r.items, id)
	return nil
}
