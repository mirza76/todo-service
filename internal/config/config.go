// Package config loads and validates service configuration from environment
// variables, following the twelve-factor app methodology. In Kubernetes these
// variables are supplied by a ConfigMap (non-sensitive) and a Secret
// (credentials).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Storage drivers supported by the service.
const (
	StorageMemory   = "memory"
	StoragePostgres = "postgres"
)

// Config is the complete, validated runtime configuration.
type Config struct {
	Port     int
	LogLevel slog.Level
	Storage  string

	HTTP     HTTPConfig
	Shutdown ShutdownConfig
	Postgres PostgresConfig
}

// HTTPConfig holds HTTP server tuning parameters.
type HTTPConfig struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	RequestTimeout    time.Duration
	MaxBodyBytes      int64
}

// ShutdownConfig controls graceful shutdown.
type ShutdownConfig struct {
	// Delay is how long the server keeps serving after it has been marked
	// not-ready, giving Kubernetes time to remove the pod from Service
	// endpoints before connections stop being accepted.
	Delay time.Duration
	// Timeout bounds how long in-flight requests may take to drain.
	Timeout time.Duration
}

// PostgresConfig holds database connection settings. Only used when
// Storage is StoragePostgres.
type PostgresConfig struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string
	SSLMode  string
}

// LookupFunc matches os.LookupEnv; injecting it keeps Load testable.
type LookupFunc func(key string) (string, bool)

// FromEnv loads configuration from the process environment.
func FromEnv() (Config, error) {
	return Load(os.LookupEnv)
}

// Load builds a Config from lookup. It reports every invalid or missing
// value at once rather than stopping at the first problem.
func Load(lookup LookupFunc) (Config, error) {
	p := parser{lookup: lookup}

	cfg := Config{
		Port:     p.int("PORT", 8080),
		LogLevel: p.logLevel("LOG_LEVEL", slog.LevelInfo),
		Storage:  p.string("STORAGE_DRIVER", StorageMemory),
		HTTP: HTTPConfig{
			ReadHeaderTimeout: p.duration("HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
			ReadTimeout:       p.duration("HTTP_READ_TIMEOUT", 10*time.Second),
			WriteTimeout:      p.duration("HTTP_WRITE_TIMEOUT", 15*time.Second),
			IdleTimeout:       p.duration("HTTP_IDLE_TIMEOUT", 60*time.Second),
			RequestTimeout:    p.duration("HTTP_REQUEST_TIMEOUT", 5*time.Second),
			MaxBodyBytes:      int64(p.int("HTTP_MAX_BODY_BYTES", 1<<20)),
		},
		Shutdown: ShutdownConfig{
			Delay:   p.duration("SHUTDOWN_DELAY", 5*time.Second),
			Timeout: p.duration("SHUTDOWN_TIMEOUT", 15*time.Second),
		},
		Postgres: PostgresConfig{
			Host:     p.string("DB_HOST", ""),
			Port:     p.int("DB_PORT", 5432),
			Database: p.string("DB_NAME", ""),
			User:     p.string("DB_USER", ""),
			Password: p.string("DB_PASSWORD", ""),
			SSLMode:  p.string("DB_SSLMODE", "disable"),
		},
	}

	if err := errors.Join(append(p.errs, cfg.validate()...)...); err != nil {
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

func (c Config) validate() []error {
	var errs []error

	if c.Port < 1 || c.Port > 65535 {
		errs = append(errs, fmt.Errorf("PORT must be between 1 and 65535, got %d", c.Port))
	}
	if c.HTTP.MaxBodyBytes < 1 {
		errs = append(errs, errors.New("HTTP_MAX_BODY_BYTES must be positive"))
	}

	// Slices (not maps) keep error messages in a deterministic order.
	positive := []struct {
		name string
		d    time.Duration
	}{
		{"HTTP_READ_HEADER_TIMEOUT", c.HTTP.ReadHeaderTimeout},
		{"HTTP_READ_TIMEOUT", c.HTTP.ReadTimeout},
		{"HTTP_WRITE_TIMEOUT", c.HTTP.WriteTimeout},
		{"HTTP_IDLE_TIMEOUT", c.HTTP.IdleTimeout},
		{"HTTP_REQUEST_TIMEOUT", c.HTTP.RequestTimeout},
		{"SHUTDOWN_TIMEOUT", c.Shutdown.Timeout},
	}
	for _, p := range positive {
		if p.d <= 0 {
			errs = append(errs, fmt.Errorf("%s must be positive, got %s", p.name, p.d))
		}
	}
	if c.Shutdown.Delay < 0 {
		errs = append(errs, fmt.Errorf("SHUTDOWN_DELAY must not be negative, got %s", c.Shutdown.Delay))
	}
	// A request must be able to finish before the server gives up writing it.
	if c.HTTP.RequestTimeout > 0 && c.HTTP.WriteTimeout > 0 && c.HTTP.RequestTimeout >= c.HTTP.WriteTimeout {
		errs = append(errs, errors.New("HTTP_REQUEST_TIMEOUT must be shorter than HTTP_WRITE_TIMEOUT"))
	}

	switch c.Storage {
	case StorageMemory:
	case StoragePostgres:
		errs = append(errs, c.Postgres.validate()...)
	default:
		errs = append(errs, fmt.Errorf("STORAGE_DRIVER must be %q or %q, got %q", StorageMemory, StoragePostgres, c.Storage))
	}

	return errs
}

func (p PostgresConfig) validate() []error {
	var errs []error
	required := []struct{ name, value string }{
		{"DB_HOST", p.Host},
		{"DB_NAME", p.Database},
		{"DB_USER", p.User},
		{"DB_PASSWORD", p.Password},
	}
	for _, r := range required {
		if r.value == "" {
			errs = append(errs, fmt.Errorf("%s is required when STORAGE_DRIVER=%s", r.name, StoragePostgres))
		}
	}
	if p.Port < 1 || p.Port > 65535 {
		errs = append(errs, fmt.Errorf("DB_PORT must be between 1 and 65535, got %d", p.Port))
	}
	switch p.SSLMode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		errs = append(errs, fmt.Errorf("DB_SSLMODE %q is not a valid PostgreSQL sslmode", p.SSLMode))
	}
	return errs
}

// parser reads typed values and accumulates parse errors.
type parser struct {
	lookup LookupFunc
	errs   []error
}

func (p *parser) raw(key string) (string, bool) {
	v, ok := p.lookup(key)
	v = strings.TrimSpace(v)
	return v, ok && v != ""
}

func (p *parser) string(key, def string) string {
	if v, ok := p.raw(key); ok {
		return v
	}
	return def
}

func (p *parser) int(key string, def int) int {
	v, ok := p.raw(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s must be an integer, got %q", key, v))
		return def
	}
	return n
}

func (p *parser) duration(key string, def time.Duration) time.Duration {
	v, ok := p.raw(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s must be a duration such as 5s or 1m, got %q", key, v))
		return def
	}
	return d
}

func (p *parser) logLevel(key string, def slog.Level) slog.Level {
	v, ok := p.raw(key)
	if !ok {
		return def
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(v)); err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s must be one of debug, info, warn, error, got %q", key, v))
		return def
	}
	return lvl
}
