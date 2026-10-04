// Command todo-api runs the ToDo REST service.
//
// main is the composition root: it loads configuration, constructs the
// concrete dependencies, wires them together, and manages the process
// lifecycle. No business logic lives here.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/mirza76/todo-service/internal/config"
	"github.com/mirza76/todo-service/internal/health"
	"github.com/mirza76/todo-service/internal/httpapi"
	"github.com/mirza76/todo-service/internal/requestid"
	"github.com/mirza76/todo-service/internal/service"
	"github.com/mirza76/todo-service/internal/storage/memory"
	"github.com/mirza76/todo-service/internal/storage/postgres"
	"github.com/mirza76/todo-service/internal/todo"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

// readinessTimeout bounds a single readiness evaluation. It must be shorter
// than the probe's timeoutSeconds in the Kubernetes manifest.
const readinessTimeout = 2 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "todo-api: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// SIGTERM is what Kubernetes sends; SIGINT is Ctrl-C locally.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}

	logger := slog.New(requestid.NewLogHandler(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}),
	))
	logger.Info("starting todo-api",
		slog.String("version", version),
		slog.String("storage", cfg.Storage),
		slog.Int("port", cfg.Port),
	)

	store, err := openStorage(ctx, cfg, logger)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer store.close()

	checker := health.New(logger, readinessTimeout, store.checks...)
	handler := httpapi.NewHandler(httpapi.Config{
		Service:        service.New(store.repo),
		Logger:         logger,
		Liveness:       checker.Live,
		Readiness:      checker.Ready,
		MaxBodyBytes:   cfg.HTTP.MaxBodyBytes,
		RequestTimeout: cfg.HTTP.RequestTimeout,
	})

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout, // mitigates Slowloris
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	// Binding before serving surfaces "address already in use" immediately.
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", net.JoinHostPort("", strconv.Itoa(cfg.Port)))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	return serve(ctx, stop, srv, ln, checker, cfg.Shutdown, logger)
}

// storage bundles a repository with its readiness checks and cleanup.
type storage struct {
	repo   todo.Repository
	checks []health.NamedCheck
	close  func()
}

func openStorage(ctx context.Context, cfg config.Config, logger *slog.Logger) (storage, error) {
	switch cfg.Storage {
	case config.StorageMemory:
		logger.Warn("using in-memory storage: data is not persisted or shared between replicas")
		return storage{repo: memory.New(), close: func() {}}, nil

	case config.StoragePostgres:
		pool, err := postgres.Connect(ctx, cfg.Postgres, logger)
		if err != nil {
			return storage{}, err
		}
		applied, err := postgres.Migrate(ctx, pool)
		if err != nil {
			pool.Close()
			return storage{}, fmt.Errorf("migrate: %w", err)
		}
		logger.Info("postgres ready", slog.Any("migrations_applied", applied))
		return storage{
			repo:   postgres.New(pool),
			checks: []health.NamedCheck{{Name: "postgres", Check: pool.Ping}},
			close:  pool.Close,
		}, nil

	default:
		// Unreachable: config.Load validates the driver.
		return storage{}, fmt.Errorf("unknown storage driver %q", cfg.Storage)
	}
}

// serve runs srv on ln until ctx is canceled, then shuts down gracefully:
//
//  1. Mark the instance not-ready so Kubernetes removes it from Service
//     endpoints.
//  2. Keep serving for sc.Delay while that removal propagates, so no new
//     connections are refused mid-rollout.
//  3. Stop accepting connections and wait up to sc.Timeout for in-flight
//     requests to complete.
//
// Delay + Timeout must stay below the pod's terminationGracePeriodSeconds.
func serve(
	ctx context.Context,
	stopSignals context.CancelFunc,
	srv *http.Server,
	ln net.Listener,
	checker *health.Checker,
	sc config.ShutdownConfig,
	logger *slog.Logger,
) error {
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	logger.Info("http server listening", slog.String("addr", ln.Addr().String()))

	select {
	case err := <-serveErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	// Restore default signal handling: a second Ctrl-C now exits immediately.
	stopSignals()
	logger.Info("shutdown started",
		slog.String("drain_delay", sc.Delay.String()),
		slog.String("timeout", sc.Timeout.String()),
	)
	checker.SetShuttingDown()

	if sc.Delay > 0 {
		time.Sleep(sc.Delay)
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sc.Timeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}

	logger.Info("shutdown complete")
	return nil
}
