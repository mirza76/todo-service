// Package requestid assigns every HTTP request a correlation ID, returns it
// in the X-Request-ID response header, and attaches it to every log record
// written with the request's context. A client or upstream proxy can supply
// its own ID so a request can be traced across services.
package requestid

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

// Header is the HTTP header that carries the request ID.
const Header = "X-Request-ID"

const maxLength = 128

type contextKey struct{}

// FromContext returns the request ID stored in ctx, or "" if there is none.
func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}

// WithID returns a copy of ctx carrying id.
func WithID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// Middleware reuses a well-formed incoming X-Request-ID or generates a new
// one, echoes it in the response, and stores it in the request context.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(Header)
		if !valid(id) {
			id = uuid.NewString()
		}
		w.Header().Set(Header, id)
		next.ServeHTTP(w, r.WithContext(WithID(r.Context(), id)))
	})
}

// valid accepts only short IDs made of URL- and log-safe characters, so a
// client cannot inject newlines, quotes, or huge values into logs.
func valid(id string) bool {
	if id == "" || len(id) > maxLength {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == ':':
		default:
			return false
		}
	}
	return true
}

// NewLogHandler wraps h so that records logged with a context carrying a
// request ID include a "request_id" attribute.
func NewLogHandler(h slog.Handler) slog.Handler {
	return logHandler{h}
}

type logHandler struct {
	slog.Handler
}

func (h logHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := FromContext(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return logHandler{h.Handler.WithAttrs(attrs)}
}

func (h logHandler) WithGroup(name string) slog.Handler {
	return logHandler{h.Handler.WithGroup(name)}
}
