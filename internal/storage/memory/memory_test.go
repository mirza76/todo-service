package memory_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mirza76/todo-service/internal/storage/memory"
	"github.com/mirza76/todo-service/internal/storage/storagetest"
	"github.com/mirza76/todo-service/internal/todo"
)

func TestRepositoryContract(t *testing.T) {
	storagetest.Run(t, func(*testing.T) todo.Repository { return memory.New() })
}

// TestConcurrentAccess is meaningful under `go test -race`: it fails if any
// method touches the map without holding the lock.
func TestConcurrentAccess(t *testing.T) {
	repo := memory.New()
	ctx := t.Context()
	now := time.Now().UTC()

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			item, err := todo.New(uuid.New(), "concurrent", false, now)
			if err != nil {
				t.Errorf("todo.New(): %v", err)
				return
			}
			if err := repo.Create(ctx, item); err != nil {
				t.Errorf("Create(): %v", err)
				return
			}
			_, _ = repo.Get(ctx, item.ID)
			_, _ = repo.List(ctx, todo.ListParams{Limit: 10})
			expected := item.Version
			_ = item.Replace("updated", true, now)
			_ = repo.Update(ctx, item, expected)
			_ = repo.Delete(ctx, item.ID, todo.AnyVersion)
		})
	}
	wg.Wait()

	page, err := repo.List(ctx, todo.ListParams{Limit: 100})
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if page.Total != 0 {
		t.Errorf("Total = %d after all deletes, want 0", page.Total)
	}
}
