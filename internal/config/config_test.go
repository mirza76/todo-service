package config_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/mirza76/todo-service/internal/config"
)

func env(vars map[string]string) config.LookupFunc {
	return func(key string) (string, bool) {
		v, ok := vars[key]
		return v, ok
	}
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := config.Load(env(nil))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want INFO", cfg.LogLevel)
	}
	if cfg.Storage != config.StorageMemory {
		t.Errorf("Storage = %q, want %q", cfg.Storage, config.StorageMemory)
	}
	if cfg.HTTP.RequestTimeout != 5*time.Second {
		t.Errorf("HTTP.RequestTimeout = %s, want 5s", cfg.HTTP.RequestTimeout)
	}
	if cfg.HTTP.MaxBodyBytes != 1<<20 {
		t.Errorf("HTTP.MaxBodyBytes = %d, want %d", cfg.HTTP.MaxBodyBytes, 1<<20)
	}
}

func TestLoad_Overrides(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"PORT":                 " 9090 ",
		"LOG_LEVEL":            "debug",
		"HTTP_REQUEST_TIMEOUT": "2s",
		"SHUTDOWN_DELAY":       "0s",
		"STORAGE_DRIVER":       "postgres",
		"DB_HOST":              "db",
		"DB_NAME":              "todos",
		"DB_USER":              "todo",
		"DB_PASSWORD":          "secret",
	}))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want 9090", cfg.Port)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want DEBUG", cfg.LogLevel)
	}
	if cfg.HTTP.RequestTimeout != 2*time.Second {
		t.Errorf("HTTP.RequestTimeout = %s, want 2s", cfg.HTTP.RequestTimeout)
	}
	if cfg.Shutdown.Delay != 0 {
		t.Errorf("Shutdown.Delay = %s, want 0s", cfg.Shutdown.Delay)
	}
	if cfg.Postgres.Host != "db" || cfg.Postgres.Port != 5432 || cfg.Postgres.SSLMode != "disable" {
		t.Errorf("Postgres = %+v, want host=db port=5432 sslmode=disable", cfg.Postgres)
	}
}

func TestLoad_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		vars    map[string]string
		wantErr []string
	}{
		{
			name:    "non-numeric port",
			vars:    map[string]string{"PORT": "abc"},
			wantErr: []string{"PORT must be an integer"},
		},
		{
			name:    "port out of range",
			vars:    map[string]string{"PORT": "70000"},
			wantErr: []string{"PORT must be between 1 and 65535"},
		},
		{
			name:    "bad log level",
			vars:    map[string]string{"LOG_LEVEL": "loud"},
			wantErr: []string{"LOG_LEVEL must be one of"},
		},
		{
			name:    "bad duration",
			vars:    map[string]string{"HTTP_READ_TIMEOUT": "10"},
			wantErr: []string{"HTTP_READ_TIMEOUT must be a duration"},
		},
		{
			name:    "zero timeout",
			vars:    map[string]string{"SHUTDOWN_TIMEOUT": "0s"},
			wantErr: []string{"SHUTDOWN_TIMEOUT must be positive"},
		},
		{
			name:    "request timeout not shorter than write timeout",
			vars:    map[string]string{"HTTP_REQUEST_TIMEOUT": "20s", "HTTP_WRITE_TIMEOUT": "20s"},
			wantErr: []string{"HTTP_REQUEST_TIMEOUT must be shorter than HTTP_WRITE_TIMEOUT"},
		},
		{
			name:    "unknown storage driver",
			vars:    map[string]string{"STORAGE_DRIVER": "bolt"},
			wantErr: []string{`STORAGE_DRIVER must be "memory" or "postgres"`},
		},
		{
			name: "postgres without credentials reports every missing field",
			vars: map[string]string{"STORAGE_DRIVER": "postgres"},
			wantErr: []string{
				"DB_HOST is required", "DB_NAME is required",
				"DB_USER is required", "DB_PASSWORD is required",
			},
		},
		{
			name: "postgres with invalid sslmode",
			vars: map[string]string{
				"STORAGE_DRIVER": "postgres", "DB_HOST": "h", "DB_NAME": "n",
				"DB_USER": "u", "DB_PASSWORD": "p", "DB_SSLMODE": "maybe",
			},
			wantErr: []string{"not a valid PostgreSQL sslmode"},
		},
		{
			name:    "multiple problems are reported together",
			vars:    map[string]string{"PORT": "0", "LOG_LEVEL": "nope"},
			wantErr: []string{"PORT must be between", "LOG_LEVEL must be one of"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(env(tt.vars))
			if err == nil {
				t.Fatal("Load() expected error, got nil")
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}
