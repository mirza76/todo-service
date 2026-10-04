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
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// Replace overwrites all client-controlled fields (PUT semantics). The ID and
// CreatedAt are immutable. On validation failure t is left unchanged.
func (t *Todo) Replace(title string, completed bool, now time.Time) error {
	title, err := normalizeTitle(title)
	if err != nil {
		return err
	}
	t.Title = title
	t.Completed = completed
	t.UpdatedAt = now
	return nil
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
