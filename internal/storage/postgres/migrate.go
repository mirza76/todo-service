package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationLockID is an arbitrary application-wide key for the advisory lock
// that serializes migrations.
const migrationLockID = 7_420_250_001

// Migrate applies pending SQL migrations from the embedded migrations
// directory, in lexical filename order.
//
// Several replicas start at the same time, so migrations run inside a single
// transaction holding a transaction-scoped advisory lock: one replica
// applies them while the others wait, then find nothing left to do.
// PostgreSQL DDL is transactional, so a failing migration leaves no partial
// schema behind.
func Migrate(ctx context.Context, pool *pgxpool.Pool) (applied []string, err error) {
	names, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	slices.Sort(names)

	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
			return fmt.Errorf("acquire migration lock: %w", err)
		}
		if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT        PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
			return fmt.Errorf("create schema_migrations: %w", err)
		}

		for _, name := range names {
			version := strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")

			var exists bool
			if err := tx.QueryRow(ctx,
				"SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", version,
			).Scan(&exists); err != nil {
				return fmt.Errorf("check migration %s: %w", version, err)
			}
			if exists {
				continue
			}

			sql, err := migrationFiles.ReadFile(name)
			if err != nil {
				return fmt.Errorf("read migration %s: %w", version, err)
			}
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return fmt.Errorf("apply migration %s: %w", version, err)
			}
			if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version); err != nil {
				return fmt.Errorf("record migration %s: %w", version, err)
			}
			applied = append(applied, version)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return applied, nil
}
