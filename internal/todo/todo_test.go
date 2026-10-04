package todo_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mirza76/todo-service/internal/todo"
)

var (
	testID   = uuid.MustParse("01920000-0000-7000-8000-000000000001")
	testTime = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
)

func TestNew_Valid(t *testing.T) {
	tests := []struct {
		name      string
		title     string
		wantTitle string
	}{
		{"simple", "Buy milk", "Buy milk"},
		{"surrounding whitespace trimmed", "  Buy milk \t", "Buy milk"},
		{"inner whitespace kept", "Buy  milk", "Buy  milk"},
		{"max length ascii", strings.Repeat("a", todo.MaxTitleLength), strings.Repeat("a", todo.MaxTitleLength)},
		{"max length counts characters not bytes", strings.Repeat("é", todo.MaxTitleLength), strings.Repeat("é", todo.MaxTitleLength)},
		{"unicode", "買い物 🛒", "買い物 🛒"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := todo.New(testID, tt.title, true, testTime)
			if err != nil {
				t.Fatalf("New() unexpected error: %v", err)
			}
			want := todo.Todo{ID: testID, Title: tt.wantTitle, Completed: true, CreatedAt: testTime, UpdatedAt: testTime}
			if got != want {
				t.Errorf("New() = %+v, want %+v", got, want)
			}
		})
	}
}

func TestNew_InvalidTitle(t *testing.T) {
	tests := []struct {
		name    string
		title   string
		wantMsg string
	}{
		{"empty", "", "must not be empty"},
		{"only whitespace", "   \n\t ", "must not be empty"},
		{"too long", strings.Repeat("a", todo.MaxTitleLength+1), "must be at most 200 characters"},
		{"invalid utf-8", "bad \xff byte", "must be valid UTF-8"},
		{"control character", "line1\nline2", "must not contain control characters"},
		{"null byte", "a\x00b", "must not contain control characters"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := todo.New(testID, tt.title, false, testTime)
			assertTitleError(t, err, tt.wantMsg)
		})
	}
}

func TestReplace(t *testing.T) {
	created, err := todo.New(testID, "Original", false, testTime)
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}
	later := testTime.Add(time.Hour)

	t.Run("valid replacement updates fields and UpdatedAt only", func(t *testing.T) {
		got := created
		if err := got.Replace("  Updated ", true, later); err != nil {
			t.Fatalf("Replace() unexpected error: %v", err)
		}
		want := todo.Todo{ID: testID, Title: "Updated", Completed: true, CreatedAt: testTime, UpdatedAt: later}
		if got != want {
			t.Errorf("after Replace() = %+v, want %+v", got, want)
		}
	})

	t.Run("invalid replacement leaves todo unchanged", func(t *testing.T) {
		got := created
		err := got.Replace("", true, later)
		assertTitleError(t, err, "must not be empty")
		if got != created {
			t.Errorf("todo mutated on failed Replace(): got %+v, want %+v", got, created)
		}
	})
}

func TestValidationError_Error(t *testing.T) {
	err := &todo.ValidationError{Fields: []todo.FieldError{
		{Field: "title", Message: "must not be empty"},
		{Field: "completed", Message: "is required"},
	}}
	want := "validation failed: title must not be empty; completed is required"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func assertTitleError(t *testing.T, err error, wantMsg string) {
	t.Helper()
	var verr *todo.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("error = %v, want *todo.ValidationError", err)
	}
	if len(verr.Fields) != 1 || verr.Fields[0].Field != "title" || verr.Fields[0].Message != wantMsg {
		t.Errorf("Fields = %+v, want [{title %s}]", verr.Fields, wantMsg)
	}
}
