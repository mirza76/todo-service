package todo

import (
	"context"

	"github.com/google/uuid"
)

// ListParams controls filtering and pagination of Repository.List. Callers
// are responsible for validating bounds; repositories trust them.
type ListParams struct {
	Limit  int
	Offset int
	// Completed, when non-nil, restricts results to todos with that status.
	Completed *bool
}

// Page is one page of todos plus the total number of todos matching the
// filter.
type Page struct {
	Items []Todo
	Total int
}

// Repository is the persistence port. Every implementation must satisfy the
// behavioral contract verified by the storagetest package:
//
//   - Get, Update, and Delete return ErrNotFound for unknown IDs.
//   - Create returns ErrAlreadyExists if the ID is already taken.
//   - Update persists Title, Completed, and UpdatedAt only; CreatedAt is
//     immutable once created.
//   - List orders by CreatedAt ascending, then ID ascending, so pagination is
//     stable, and Items is never nil. Total counts only todos matching the
//     filter.
//   - All methods honor context cancellation.
type Repository interface {
	Create(ctx context.Context, t Todo) error
	Get(ctx context.Context, id uuid.UUID) (Todo, error)
	List(ctx context.Context, params ListParams) (Page, error)
	Update(ctx context.Context, t Todo) error
	Delete(ctx context.Context, id uuid.UUID) error
}
