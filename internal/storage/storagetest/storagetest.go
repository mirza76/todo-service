// Package storagetest is a reusable contract test suite for todo.Repository.
// Every storage adapter runs the same suite, which guarantees that adapters
// are interchangeable (Liskov substitution) and that the documented contract
// on todo.Repository is actually enforced.
package storagetest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mirza76/todo-service/internal/todo"
)

// Factory returns an empty repository for a single test. Implementations
// should register any cleanup with t.Cleanup.
type Factory func(t *testing.T) todo.Repository

// baseTime is truncated to microseconds, the precision PostgreSQL stores.
var baseTime = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

// Run executes the full contract suite against repositories from newRepo.
func Run(t *testing.T, newRepo Factory) {
	t.Helper()

	tests := []struct {
		name string
		fn   func(t *testing.T, repo todo.Repository)
	}{
		{"CreateAndGet", testCreateAndGet},
		{"CreateDuplicateID", testCreateDuplicateID},
		{"GetNotFound", testGetNotFound},
		{"Update", testUpdate},
		{"UpdateKeepsCreatedAt", testUpdateKeepsCreatedAt},
		{"UpdateNotFound", testUpdateNotFound},
		{"Delete", testDelete},
		{"DeleteNotFound", testDeleteNotFound},
		{"ListEmpty", testListEmpty},
		{"ListOrdering", testListOrdering},
		{"ListPagination", testListPagination},
		{"CanceledContext", testCanceledContext},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.fn(t, newRepo(t))
		})
	}
}

func testCreateAndGet(t *testing.T, repo todo.Repository) {
	ctx := t.Context()
	want := mustCreate(t, repo, newTodo(t, uuid.New(), "Write tests", baseTime))

	got, err := repo.Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	assertTodoEqual(t, got, want)
}

func testCreateDuplicateID(t *testing.T, repo todo.Repository) {
	first := mustCreate(t, repo, newTodo(t, uuid.New(), "First", baseTime))

	dup := newTodo(t, first.ID, "Second", baseTime)
	if err := repo.Create(t.Context(), dup); !errors.Is(err, todo.ErrAlreadyExists) {
		t.Fatalf("Create() duplicate error = %v, want ErrAlreadyExists", err)
	}

	got, err := repo.Get(t.Context(), first.ID)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	assertTodoEqual(t, got, first)
}

func testGetNotFound(t *testing.T, repo todo.Repository) {
	if _, err := repo.Get(t.Context(), uuid.New()); !errors.Is(err, todo.ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func testUpdate(t *testing.T, repo todo.Repository) {
	ctx := t.Context()
	item := mustCreate(t, repo, newTodo(t, uuid.New(), "Before", baseTime))

	if err := item.Replace("After", true, baseTime.Add(time.Minute)); err != nil {
		t.Fatalf("Replace() unexpected error: %v", err)
	}
	if err := repo.Update(ctx, item); err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}

	got, err := repo.Get(ctx, item.ID)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	assertTodoEqual(t, got, item)
}

func testUpdateKeepsCreatedAt(t *testing.T, repo todo.Repository) {
	ctx := t.Context()
	item := mustCreate(t, repo, newTodo(t, uuid.New(), "Title", baseTime))

	tampered := item
	tampered.CreatedAt = baseTime.Add(-24 * time.Hour)
	tampered.UpdatedAt = baseTime.Add(time.Minute)
	if err := repo.Update(ctx, tampered); err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}

	got, err := repo.Get(ctx, item.ID)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if !got.CreatedAt.Equal(item.CreatedAt) {
		t.Errorf("CreatedAt = %s, want unchanged %s", got.CreatedAt, item.CreatedAt)
	}
}

func testUpdateNotFound(t *testing.T, repo todo.Repository) {
	missing := newTodo(t, uuid.New(), "Ghost", baseTime)
	if err := repo.Update(t.Context(), missing); !errors.Is(err, todo.ErrNotFound) {
		t.Fatalf("Update() error = %v, want ErrNotFound", err)
	}
}

func testDelete(t *testing.T, repo todo.Repository) {
	ctx := t.Context()
	item := mustCreate(t, repo, newTodo(t, uuid.New(), "Doomed", baseTime))

	if err := repo.Delete(ctx, item.ID); err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
	if _, err := repo.Get(ctx, item.ID); !errors.Is(err, todo.ErrNotFound) {
		t.Fatalf("Get() after Delete() error = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, item.ID); !errors.Is(err, todo.ErrNotFound) {
		t.Fatalf("second Delete() error = %v, want ErrNotFound", err)
	}
}

func testDeleteNotFound(t *testing.T, repo todo.Repository) {
	if err := repo.Delete(t.Context(), uuid.New()); !errors.Is(err, todo.ErrNotFound) {
		t.Fatalf("Delete() error = %v, want ErrNotFound", err)
	}
}

func testListEmpty(t *testing.T, repo todo.Repository) {
	page, err := repo.List(t.Context(), todo.ListParams{Limit: 10})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if page.Items == nil {
		t.Error("List() Items is nil, want empty non-nil slice")
	}
	if len(page.Items) != 0 || page.Total != 0 {
		t.Errorf("List() = %d items, total %d; want 0, 0", len(page.Items), page.Total)
	}
}

func testListOrdering(t *testing.T, repo todo.Repository) {
	// Created out of order; two share a timestamp so ID breaks the tie.
	idLow := uuid.MustParse("00000000-0000-7000-8000-000000000001")
	idHigh := uuid.MustParse("00000000-0000-7000-8000-000000000002")
	later := mustCreate(t, repo, newTodo(t, uuid.New(), "later", baseTime.Add(time.Hour)))
	tieHigh := mustCreate(t, repo, newTodo(t, idHigh, "tie high", baseTime))
	tieLow := mustCreate(t, repo, newTodo(t, idLow, "tie low", baseTime))
	earliest := mustCreate(t, repo, newTodo(t, uuid.New(), "earliest", baseTime.Add(-time.Hour)))

	page, err := repo.List(t.Context(), todo.ListParams{Limit: 10})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	assertIDs(t, page.Items, earliest, tieLow, tieHigh, later)
}

func testListPagination(t *testing.T, repo todo.Repository) {
	all := make([]todo.Todo, 5)
	for i := range all {
		all[i] = mustCreate(t, repo, newTodo(t, uuid.New(), fmt.Sprintf("item %d", i), baseTime.Add(time.Duration(i)*time.Second)))
	}

	tests := []struct {
		name   string
		params todo.ListParams
		want   []todo.Todo
	}{
		{"first page", todo.ListParams{Limit: 2, Offset: 0}, all[0:2]},
		{"middle page", todo.ListParams{Limit: 2, Offset: 2}, all[2:4]},
		{"partial last page", todo.ListParams{Limit: 2, Offset: 4}, all[4:5]},
		{"offset past end", todo.ListParams{Limit: 2, Offset: 10}, nil},
		{"limit larger than total", todo.ListParams{Limit: 100, Offset: 0}, all},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := repo.List(t.Context(), tt.params)
			if err != nil {
				t.Fatalf("List() unexpected error: %v", err)
			}
			if page.Total != len(all) {
				t.Errorf("Total = %d, want %d", page.Total, len(all))
			}
			if page.Items == nil {
				t.Error("Items is nil, want non-nil slice")
			}
			assertIDs(t, page.Items, tt.want...)
		})
	}
}

func testCanceledContext(t *testing.T, repo todo.Repository) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	item := newTodo(t, uuid.New(), "Never stored", baseTime)
	checks := map[string]error{
		"Create": repo.Create(ctx, item),
		"Update": repo.Update(ctx, item),
		"Delete": repo.Delete(ctx, item.ID),
	}
	_, checks["Get"] = repo.Get(ctx, item.ID)
	_, checks["List"] = repo.List(ctx, todo.ListParams{Limit: 1})

	for op, err := range checks {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%s() with canceled context error = %v, want context.Canceled", op, err)
		}
	}
}

// --- helpers ---

func newTodo(t *testing.T, id uuid.UUID, title string, at time.Time) todo.Todo {
	t.Helper()
	item, err := todo.New(id, title, false, at)
	if err != nil {
		t.Fatalf("todo.New() unexpected error: %v", err)
	}
	return item
}

func mustCreate(t *testing.T, repo todo.Repository, item todo.Todo) todo.Todo {
	t.Helper()
	if err := repo.Create(t.Context(), item); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	return item
}

// assertTodoEqual compares field by field, using time.Equal so that
// differences in time.Location (e.g. UTC vs. Local from a DB driver) don't
// cause false failures.
func assertTodoEqual(t *testing.T, got, want todo.Todo) {
	t.Helper()
	if got.ID != want.ID || got.Title != want.Title || got.Completed != want.Completed ||
		!got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("todo mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

func assertIDs(t *testing.T, got []todo.Todo, want ...todo.Todo) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d items, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID {
			t.Errorf("item[%d] = %s (%q), want %s (%q)", i, got[i].ID, got[i].Title, want[i].ID, want[i].Title)
		}
	}
}
