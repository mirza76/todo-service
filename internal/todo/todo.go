// Package todo is the domain core: the Todo entity, its invariants, domain
// errors, and the Repository port that storage adapters implement. It has no
// knowledge of HTTP, SQL, or any other delivery or infrastructure concern.
package todo

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// MaxTitleLength is the maximum title length in characters (Unicode code
// points), not bytes, so multi-byte scripts get the same allowance.
const MaxTitleLength = 200

// Todo is a single task. A Todo obtained from New or Replace always satisfies
// the domain invariants (non-empty, bounded, printable title).
type Todo struct {
	ID        uuid.UUID
	Title     string
	Completed bool
	// Version starts at 1 and increases by one on every change. It drives
	// optimistic concurrency control (ETag / If-Match).
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// New creates a validated Todo. The ID and timestamp are supplied by the
// caller so that the domain stays deterministic and easy to test.
func New(id uuid.UUID, title string, completed bool, now time.Time) (Todo, error) {
	title, err := normalizeTitle(title)
	if err != nil {
		return Todo{}, err
	}
	return Todo{
		ID:        id,
		Title:     title,
		Completed: completed,
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// Replace overwrites all client-controlled fields (PUT semantics) and bumps
// the version. The ID and CreatedAt are immutable. On validation failure t
// is left unchanged.
func (t *Todo) Replace(title string, completed bool, now time.Time) error {
	title, err := normalizeTitle(title)
	if err != nil {
		return err
	}
	t.Title = title
	t.Completed = completed
	t.Version++
	t.UpdatedAt = now
	return nil
}

// Patch holds a partial update; nil fields are left unchanged.
type Patch struct {
	Title     *string
	Completed *bool
}

// IsEmpty reports whether the patch changes nothing.
func (p Patch) IsEmpty() bool {
	return p.Title == nil && p.Completed == nil
}

// Apply merges p into t and validates the result with the same rules as
// Replace. On validation failure t is left unchanged.
func (t *Todo) Apply(p Patch, now time.Time) error {
	title, completed := t.Title, t.Completed
	if p.Title != nil {
		title = *p.Title
	}
	if p.Completed != nil {
		completed = *p.Completed
	}
	return t.Replace(title, completed, now)
}

// normalizeTitle trims surrounding whitespace and enforces title invariants.
func normalizeTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	switch {
	case title == "":
		return "", titleError("must not be empty")
	case !utf8.ValidString(title):
		return "", titleError("must be valid UTF-8")
	case utf8.RuneCountInString(title) > MaxTitleLength:
		return "", titleError("must be at most 200 characters")
	case strings.IndexFunc(title, unicode.IsControl) >= 0:
		return "", titleError("must not contain control characters")
	}
	return title, nil
}
