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

	got, err := svc.Replace(t.Context(), created.ID, service.ReplaceInput{Title: "Final", Completed: true})
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
		_, err := svc.Replace(t.Context(), uuid.New(), service.ReplaceInput{Title: "x"})
		if !errors.Is(err, todo.ErrNotFound) {
			t.Fatalf("Replace() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("invalid title leaves stored todo unchanged", func(t *testing.T) {
		_, err := svc.Replace(t.Context(), created.ID, service.ReplaceInput{Title: ""})
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

func TestDelete(t *testing.T) {
	svc, _ := newService(t)
	created, err := svc.Create(t.Context(), service.CreateInput{Title: "Temp"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if err := svc.Delete(t.Context(), created.ID); err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
	if _, err := svc.Get(t.Context(), created.ID); !errors.Is(err, todo.ErrNotFound) {
		t.Fatalf("Get() after Delete() error = %v, want ErrNotFound", err)
	}
	if err := svc.Delete(t.Context(), created.ID); !errors.Is(err, todo.ErrNotFound) {
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
