// Package postgres provides a PostgreSQL-backed todo.Repository. It is the
// production storage: unlike the in-memory adapter, all replicas share one
// consistent dataset that survives restarts.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mirza76/todo-service/internal/config"
	"github.com/mirza76/todo-service/internal/todo"
)

// uniqueViolation is the SQLSTATE for a unique constraint violation.
const uniqueViolation = "23505"

// Connect creates a connection pool and waits, with exponential backoff,
// until the database answers or cfg.ConnectTimeout elapses. Waiting smooths
// over the common case where the app starts before the database; giving up
// lets the orchestrator restart the process instead of hanging forever.
func Connect(ctx context.Context, cfg config.PostgresConfig, logger *slog.Logger) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(connString(cfg))
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}
	poolCfg.MaxConns = int32(min(cfg.MaxConns, 1<<15)) //nolint:gosec // bounded above

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	backoff := 250 * time.Millisecond
	const maxBackoff = 5 * time.Second
	for attempt := 1; ; attempt++ {
		err := pool.Ping(ctx)
		if err == nil {
			return pool, nil
		}
		logger.WarnContext(ctx, "postgres not reachable yet",
			slog.Int("attempt", attempt),
			slog.String("retry_in", backoff.String()),
			slog.Any("error", err),
		)
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, fmt.Errorf("postgres not reachable within %s: %w", cfg.ConnectTimeout, err)
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// connString builds a URL so that credentials containing special characters
// are escaped correctly.
func connString(cfg config.PostgresConfig) string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:     "/" + cfg.Database,
		RawQuery: url.Values{"sslmode": {cfg.SSLMode}}.Encode(),
	}
	return u.String()
}

// Repository implements todo.Repository on PostgreSQL.
type Repository struct {
	pool *pgxpool.Pool
}

// Compile-time check that Repository satisfies the port.
var _ todo.Repository = (*Repository)(nil)

// New returns a Repository using pool. The schema must already be migrated.
func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Create inserts t, mapping a primary key conflict to todo.ErrAlreadyExists.
func (r *Repository) Create(ctx context.Context, t todo.Todo) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO todos (id, title, completed, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		t.ID, t.Title, t.Completed, t.CreatedAt, t.UpdatedAt,
	)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == uniqueViolation {
		return todo.ErrAlreadyExists
	}
	return err
}

// Get returns the todo with the given ID.
func (r *Repository) Get(ctx context.Context, id uuid.UUID) (todo.Todo, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, title, completed, created_at, updated_at FROM todos WHERE id = $1`, id)
	if err != nil {
		return todo.Todo{}, err
	}
	t, err := pgx.CollectExactlyOneRow(rows, scanTodo)
	if errors.Is(err, pgx.ErrNoRows) {
		return todo.Todo{}, todo.ErrNotFound
	}
	return t, err
}

// List returns one page of todos ordered by created_at, then id. The page
// and the total are read in one REPEATABLE READ snapshot so they agree even
// while other replicas are writing.
func (r *Repository) List(ctx context.Context, params todo.ListParams) (todo.Page, error) {
	var page todo.Page
	txOpts := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	err := pgx.BeginTxFunc(ctx, r.pool, txOpts, func(tx pgx.Tx) error {
		// A NULL $1 disables the completed filter.
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM todos WHERE ($1::boolean IS NULL OR completed = $1)`,
			params.Completed,
		).Scan(&page.Total); err != nil {
			return fmt.Errorf("count todos: %w", err)
		}
		rows, err := tx.Query(ctx,
			`SELECT id, title, completed, created_at, updated_at FROM todos
			 WHERE ($1::boolean IS NULL OR completed = $1)
			 ORDER BY created_at, id
			 LIMIT $2 OFFSET $3`,
			params.Completed, params.Limit, params.Offset)
		if err != nil {
			return fmt.Errorf("query todos: %w", err)
		}
		page.Items, err = pgx.CollectRows(rows, scanTodo)
		return err
	})
	if err != nil {
		return todo.Page{}, err
	}
	if page.Items == nil {
		page.Items = []todo.Todo{}
	}
	return page, nil
}

// Update overwrites title, completed, and updated_at. created_at is never
// written after insert.
func (r *Repository) Update(ctx context.Context, t todo.Todo) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE todos SET title = $2, completed = $3, updated_at = $4 WHERE id = $1`,
		t.ID, t.Title, t.Completed, t.UpdatedAt,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return todo.ErrNotFound
	}
	return nil
}

// Delete removes the todo with the given ID.
func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM todos WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return todo.ErrNotFound
	}
	return nil
}

// scanTodo maps a row to a Todo. Timestamps are normalized to UTC because
// pgx returns them in the process's local time zone.
func scanTodo(row pgx.CollectableRow) (todo.Todo, error) {
	var t todo.Todo
	if err := row.Scan(&t.ID, &t.Title, &t.Completed, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return todo.Todo{}, err
	}
	t.CreatedAt = t.CreatedAt.UTC()
	t.UpdatedAt = t.UpdatedAt.UTC()
	return t, nil
}
