package requestid_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mirza76/todo-service/internal/requestid"
)

func TestMiddleware(t *testing.T) {
	tests := []struct {
		name     string
		incoming string
		wantKeep bool
	}{
		{"generates when absent", "", false},
		{"keeps a well-formed id", "abc-123_DEF.ghi:42", true},
		{"replaces id with spaces", "abc 123", false},
		{"replaces id with newline (log injection)", "abc\nlevel=ERROR", false},
		{"replaces id with quotes", `abc"def`, false},
		{"replaces overly long id", strings.Repeat("a", 129), false},
		{"keeps max-length id", strings.Repeat("a", 128), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen string
			h := requestid.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				seen = requestid.FromContext(r.Context())
			}))

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			if tt.incoming != "" {
				req.Header.Set(requestid.Header, tt.incoming)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			got := rec.Header().Get(requestid.Header)
			if got != seen {
				t.Errorf("response header %q != context id %q", got, seen)
			}
			if tt.wantKeep {
				if got != tt.incoming {
					t.Errorf("id = %q, want incoming %q kept", got, tt.incoming)
				}
				return
			}
			if _, err := uuid.Parse(got); err != nil {
				t.Errorf("id = %q, want a generated UUID", got)
			}
		})
	}
}

func TestFromContext_Empty(t *testing.T) {
	if id := requestid.FromContext(t.Context()); id != "" {
		t.Errorf("FromContext() = %q, want empty", id)
	}
}

func TestLogHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(requestid.NewLogHandler(slog.NewJSONHandler(&buf, nil))).
		With(slog.String("component", "test")) // WithAttrs must keep the wrapper

	logger.InfoContext(requestid.WithID(t.Context(), "req-1"), "with id")
	logger.InfoContext(t.Context(), "without id")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want 2", len(lines))
	}

	var first, second map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if first["request_id"] != "req-1" || first["component"] != "test" {
		t.Errorf("first record = %v, want request_id=req-1 and component=test", first)
	}
	if _, ok := second["request_id"]; ok {
		t.Errorf("second record = %v, want no request_id", second)
	}
}
