package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mirza76/todo-service/internal/service"
	"github.com/mirza76/todo-service/internal/storage/memory"
	"github.com/mirza76/todo-service/internal/todo"
)

// fakeClock returns a fixed instant that tests can advance.
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func newService(t *testing.T) (*service.Service, *fakeClock) {
	t.Helper()
	// Local zone and sub-microsecond precision prove timestamp() normalizes.
	clock := &fakeClock{t: time.Date(2026, 10, 5, 12, 0, 0, 123456789, time.FixedZone("PKT", 5*3600))}
	return service.New(memory.New(), service.WithClock(clock.Now)), clock
}

func TestCreate(t *testing.T) {
	svc, clock := newService(t)

	got, err := svc.Create(t.Context(), service.CreateInput{Title: "  Ship it  ", Completed: true})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	wantTime := clock.Now().UTC().Truncate(time.Microsecond)
	if got.ID.Version() != 7 {
		t.Errorf("ID version = %d, want 7", got.ID.Version())
	}
	if got.Title != "Ship it" || !got.Completed {
		t.Errorf("got title %q completed %v, want %q true", got.Title, got.Completed, "Ship it")
	}
	if !got.CreatedAt.Equal(wantTime) || got.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt = %v, want %v in UTC", got.CreatedAt, wantTime)
	}

	stored, err := svc.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if stored != got {
		t.Errorf("stored = %+v, want %+v", stored, got)
	}
}

func TestCreate_ValidationError(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.Create(t.Context(), service.CreateInput{Title: "   "})
	var verr *todo.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("Create() error = %v, want *todo.ValidationError", err)
	}

	page, err := svc.List(t.Context(), todo.ListParams{Limit: 10})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if page.Total != 0 {
		t.Errorf("invalid todo was stored: total = %d", page.Total)
	}
}

func TestCreate_IDGeneratorFailure(t *testing.T) {
	boom := errors.New("entropy exhausted")
	svc := service.New(memory.New(), service.WithIDGenerator(func() (uuid.UUID, error) {
		return uuid.Nil, boom
	}))

	if _, err := svc.Create(t.Context(), service.CreateInput{Title: "x"}); !errors.Is(err, boom) {
		t.Fatalf("Create() error = %v, want wrapped %v", err, boom)
	}
}

func TestReplace(t *testing.T) {
	svc, clock := newService(t)
	created, err := svc.Create(t.Context(), service.CreateInput{Title: "Draft"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	clock.Advance(time.Minute)

	got, err := svc.Replace(t.Context(), created.ID, service.ReplaceInput{Title: "Final", Completed: true}, service.IfMatch{})
	if err != nil {
		t.Fatalf("Replace() unexpected error: %v", err)
	}

	if got.Title != "Final" || !got.Completed {
		t.Errorf("got title %q completed %v, want %q true", got.Title, got.Completed, "Final")
	}
	if !got.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("CreatedAt changed: %v -> %v", created.CreatedAt, got.CreatedAt)
	}
	if !got.UpdatedAt.Equal(created.UpdatedAt.Add(time.Minute)) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, created.UpdatedAt.Add(time.Minute))
	}

	stored, err := svc.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if stored != got {
		t.Errorf("stored = %+v, want %+v", stored, got)
	}
}

func TestReplace_Errors(t *testing.T) {
	svc, _ := newService(t)
	created, err := svc.Create(t.Context(), service.CreateInput{Title: "Keep me"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	t.Run("not found", func(t *testing.T) {
		_, err := svc.Replace(t.Context(), uuid.New(), service.ReplaceInput{Title: "x"}, service.IfMatch{})
		if !errors.Is(err, todo.ErrNotFound) {
			t.Fatalf("Replace() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("invalid title leaves stored todo unchanged", func(t *testing.T) {
		_, err := svc.Replace(t.Context(), created.ID, service.ReplaceInput{Title: ""}, service.IfMatch{})
		var verr *todo.ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("Replace() error = %v, want *todo.ValidationError", err)
		}
		stored, err := svc.Get(t.Context(), created.ID)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		if stored != created {
			t.Errorf("stored = %+v, want unchanged %+v", stored, created)
		}
	})
}

func TestUpdate(t *testing.T) {
	svc, clock := newService(t)
	created, err := svc.Create(t.Context(), service.CreateInput{Title: "Draft"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	done := true

	t.Run("partial update changes only given fields", func(t *testing.T) {
		clock.Advance(time.Minute)
		got, err := svc.Update(t.Context(), created.ID, todo.Patch{Completed: &done}, service.IfMatch{})
		if err != nil {
			t.Fatalf("Update() unexpected error: %v", err)
		}
		if got.Title != "Draft" || !got.Completed || !got.UpdatedAt.After(created.UpdatedAt) {
			t.Errorf("got %+v, want title Draft, completed true, newer updated_at", got)
		}
		stored, err := svc.Get(t.Context(), created.ID)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		if stored != got {
			t.Errorf("stored = %+v, want %+v", stored, got)
		}
	})

	t.Run("empty patch is a no-op", func(t *testing.T) {
		before, err := svc.Get(t.Context(), created.ID)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		clock.Advance(time.Minute)
		got, err := svc.Update(t.Context(), created.ID, todo.Patch{}, service.IfMatch{})
		if err != nil {
			t.Fatalf("Update() unexpected error: %v", err)
		}
		if got != before {
			t.Errorf("empty patch changed todo: got %+v, want %+v", got, before)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := svc.Update(t.Context(), uuid.New(), todo.Patch{Completed: &done}, service.IfMatch{})
		if !errors.Is(err, todo.ErrNotFound) {
			t.Fatalf("Update() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("invalid title", func(t *testing.T) {
		blank := " "
		_, err := svc.Update(t.Context(), created.ID, todo.Patch{Title: &blank}, service.IfMatch{})
		var verr *todo.ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("Update() error = %v, want *todo.ValidationError", err)
		}
	})
}

func TestPreconditions(t *testing.T) {
	svc, _ := newService(t)
	created, err := svc.Create(t.Context(), service.CreateInput{Title: "Versioned"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if created.Version != 1 {
		t.Fatalf("new todo Version = %d, want 1", created.Version)
	}
	done := true

	t.Run("stale version is rejected on every write", func(t *testing.T) {
		stale := service.MatchVersions(99)
		if _, err := svc.Replace(t.Context(), created.ID, service.ReplaceInput{Title: "x"}, stale); !errors.Is(err, todo.ErrPreconditionFailed) {
			t.Errorf("Replace() error = %v, want ErrPreconditionFailed", err)
		}
		if _, err := svc.Update(t.Context(), created.ID, todo.Patch{Completed: &done}, stale); !errors.Is(err, todo.ErrPreconditionFailed) {
			t.Errorf("Update() error = %v, want ErrPreconditionFailed", err)
		}
		if _, err := svc.Update(t.Context(), created.ID, todo.Patch{}, stale); !errors.Is(err, todo.ErrPreconditionFailed) {
			t.Errorf("empty Update() error = %v, want ErrPreconditionFailed (precondition before no-op)", err)
		}
		if err := svc.Delete(t.Context(), created.ID, stale); !errors.Is(err, todo.ErrPreconditionFailed) {
			t.Errorf("Delete() error = %v, want ErrPreconditionFailed", err)
		}
	})

	t.Run("no usable ETag never matches", func(t *testing.T) {
		if _, err := svc.Update(t.Context(), created.ID, todo.Patch{Completed: &done}, service.MatchVersions()); !errors.Is(err, todo.ErrPreconditionFailed) {
			t.Errorf("Update() error = %v, want ErrPreconditionFailed", err)
		}
	})

	t.Run("matching version succeeds and bumps the version", func(t *testing.T) {
		got, err := svc.Update(t.Context(), created.ID, todo.Patch{Completed: &done}, service.MatchVersions(5, created.Version))
		if err != nil {
			t.Fatalf("Update() unexpected error: %v", err)
		}
		if got.Version != created.Version+1 {
			t.Errorf("Version = %d, want %d", got.Version, created.Version+1)
		}
		if err := svc.Delete(t.Context(), created.ID, service.MatchVersions(got.Version)); err != nil {
			t.Fatalf("Delete() with current version unexpected error: %v", err)
		}
	})
}

// racyRepo simulates another replica writing the same todo between the
// service's read and its compare-and-set write.
type racyRepo struct {
	*memory.Repository
	interferences int // how many upcoming writes to race against
}

func (r *racyRepo) Update(ctx context.Context, t todo.Todo, expectedVersion int64) error {
	if r.interferences > 0 {
		r.interferences--
		other, err := r.Get(ctx, t.ID)
		if err != nil {
			return err
		}
		v := other.Version
		if err := other.Replace("changed by another replica", other.Completed, other.UpdatedAt); err != nil {
			return err
		}
		if err := r.Repository.Update(ctx, other, v); err != nil {
			return err
		}
	}
	return r.Repository.Update(ctx, t, expectedVersion)
}

func TestConcurrentWriteHandling(t *testing.T) {
	done := true
	setup := func(t *testing.T, interferences int) (*service.Service, todo.Todo, *racyRepo) {
		t.Helper()
		repo := &racyRepo{Repository: memory.New()}
		svc := service.New(repo)
		created, err := svc.Create(t.Context(), service.CreateInput{Title: "Original"})
		if err != nil {
			t.Fatalf("Create() unexpected error: %v", err)
		}
		repo.interferences = interferences
		return svc, created, repo
	}

	t.Run("unconditional patch is retried and both changes survive", func(t *testing.T) {
		svc, created, _ := setup(t, 1)
		got, err := svc.Update(t.Context(), created.ID, todo.Patch{Completed: &done}, service.IfMatch{})
		if err != nil {
			t.Fatalf("Update() unexpected error: %v", err)
		}
		// The other writer's title and our completed flag are both kept.
		if got.Title != "changed by another replica" || !got.Completed || got.Version != 3 {
			t.Errorf("got %+v, want other writer's title, completed=true, version 3", got)
		}
	})

	t.Run("conditional write loses the race with 412", func(t *testing.T) {
		svc, created, _ := setup(t, 1)
		_, err := svc.Update(t.Context(), created.ID, todo.Patch{Completed: &done}, service.MatchVersions(created.Version))
		if !errors.Is(err, todo.ErrPreconditionFailed) {
			t.Fatalf("Update() error = %v, want ErrPreconditionFailed", err)
		}
	})

	t.Run("gives up after repeated conflicts", func(t *testing.T) {
		svc, created, _ := setup(t, 100)
		_, err := svc.Replace(t.Context(), created.ID, service.ReplaceInput{Title: "mine"}, service.IfMatch{})
		if !errors.Is(err, todo.ErrVersionConflict) {
			t.Fatalf("Replace() error = %v, want ErrVersionConflict", err)
		}
	})
}

func TestDelete(t *testing.T) {
	svc, _ := newService(t)
	created, err := svc.Create(t.Context(), service.CreateInput{Title: "Temp"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if err := svc.Delete(t.Context(), created.ID, service.IfMatch{}); err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
	if _, err := svc.Get(t.Context(), created.ID); !errors.Is(err, todo.ErrNotFound) {
		t.Fatalf("Get() after Delete() error = %v, want ErrNotFound", err)
	}
	if err := svc.Delete(t.Context(), created.ID, service.IfMatch{}); !errors.Is(err, todo.ErrNotFound) {
		t.Fatalf("second Delete() error = %v, want ErrNotFound", err)
	}
}

func TestList(t *testing.T) {
	svc, clock := newService(t)
	for _, title := range []string{"a", "b", "c"} {
		if _, err := svc.Create(t.Context(), service.CreateInput{Title: title}); err != nil {
			t.Fatalf("Create() unexpected error: %v", err)
		}
		clock.Advance(time.Second)
	}

	page, err := svc.List(t.Context(), todo.ListParams{Limit: 2, Offset: 1})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if page.Total != 3 || len(page.Items) != 2 || page.Items[0].Title != "b" || page.Items[1].Title != "c" {
		t.Errorf("List() = total %d items %+v, want total 3 items [b c]", page.Total, page.Items)
	}
}

func TestList_InvalidParams(t *testing.T) {
	svc, _ := newService(t)

	tests := []struct {
		name       string
		params     todo.ListParams
		wantFields []string
	}{
		{"zero limit", todo.ListParams{Limit: 0}, []string{"limit"}},
		{"limit above max", todo.ListParams{Limit: service.MaxListLimit + 1}, []string{"limit"}},
		{"negative offset", todo.ListParams{Limit: 1, Offset: -1}, []string{"offset"}},
		{"both invalid", todo.ListParams{Limit: -1, Offset: -1}, []string{"limit", "offset"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.List(t.Context(), tt.params)
			var verr *todo.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("List() error = %v, want *todo.ValidationError", err)
			}
			if len(verr.Fields) != len(tt.wantFields) {
				t.Fatalf("Fields = %+v, want fields %v", verr.Fields, tt.wantFields)
			}
			for i, f := range tt.wantFields {
				if verr.Fields[i].Field != f {
					t.Errorf("Fields[%d] = %q, want %q", i, verr.Fields[i].Field, f)
				}
			}
		})
	}
}

func TestRepositoryErrorsAreWrapped(t *testing.T) {
	svc, _ := newService(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := svc.List(ctx, todo.ListParams{Limit: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("List() error = %v, want wrapped context.Canceled", err)
	}
}
