package postgres_test

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mirza76/todo-service/internal/config"
	"github.com/mirza76/todo-service/internal/storage/postgres"
	"github.com/mirza76/todo-service/internal/storage/storagetest"
	"github.com/mirza76/todo-service/internal/todo"
)

// These are integration tests against a real PostgreSQL started in Docker
// via testcontainers. Run `go test -short ./...` to skip them.

func startPostgres(t *testing.T) config.PostgresConfig {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping PostgreSQL integration test in -short mode")
	}

	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("todos"),
		tcpostgres.WithUsername("todo"),
		tcpostgres.WithPassword("p@ss:w/rd"), // special characters exercise URL escaping
		tcpostgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}
	portNum, err := strconv.Atoi(port.Port())
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	return config.PostgresConfig{
		Host: host, Port: portNum, Database: "todos", User: "todo", Password: "p@ss:w/rd",
		SSLMode: "disable", MaxConns: 10, ConnectTimeout: 30 * time.Second,
	}
}

func connect(t *testing.T, cfg config.PostgresConfig) *pgxpool.Pool {
	t.Helper()
	pool, err := postgres.Connect(t.Context(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Connect(): %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestPostgres(t *testing.T) {
	cfg := startPostgres(t)
	pool := connect(t, cfg)

	if _, err := postgres.Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate(): %v", err)
	}

	t.Run("RepositoryContract", func(t *testing.T) {
		// One database, so contract cases run sequentially on a clean table.
		storagetest.Run(t, func(t *testing.T) todo.Repository {
			if _, err := pool.Exec(t.Context(), "TRUNCATE todos"); err != nil {
				t.Fatalf("truncate: %v", err)
			}
			return postgres.New(pool)
		})
	})

	t.Run("MigrateIsIdempotent", func(t *testing.T) {
		applied, err := postgres.Migrate(t.Context(), pool)
		if err != nil {
			t.Fatalf("second Migrate(): %v", err)
		}
		if len(applied) != 0 {
			t.Errorf("second Migrate() applied %v, want nothing", applied)
		}
	})

	t.Run("ConcurrentMigrateIsSafe", func(t *testing.T) {
		// Simulates several replicas starting at once against a fresh schema.
		if _, err := pool.Exec(t.Context(), "DROP TABLE todos, schema_migrations"); err != nil {
			t.Fatalf("reset schema: %v", err)
		}

		const replicas = 5
		results := make([][]string, replicas)
		errs := make([]error, replicas)
		var wg sync.WaitGroup
		for i := range replicas {
			wg.Go(func() { results[i], errs[i] = postgres.Migrate(t.Context(), pool) })
		}
		wg.Wait()

		appliedCount := 0
		for i := range replicas {
			if errs[i] != nil {
				t.Fatalf("replica %d Migrate(): %v", i, errs[i])
			}
			if len(results[i]) > 0 {
				appliedCount++
			}
		}
		if appliedCount != 1 {
			t.Errorf("%d replicas applied migrations, want exactly 1", appliedCount)
		}
	})
}

func TestConnect_GivesUpAfterTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	cfg := config.PostgresConfig{
		Host: "127.0.0.1", Port: 1, Database: "x", User: "x", Password: "x",
		SSLMode: "disable", MaxConns: 1, ConnectTimeout: 500 * time.Millisecond,
	}

	start := time.Now()
	_, err := postgres.Connect(t.Context(), cfg, slog.New(slog.DiscardHandler))
	if err == nil {
		t.Fatal("Connect() to closed port succeeded, want error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Connect() took %s, want it to give up near the 500ms timeout", elapsed)
	}
}
