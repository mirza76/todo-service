package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// responseRecorder captures the status code and size of a response for
// logging and lets the panic handler know whether headers were already sent.
type responseRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func recordResponse(w http.ResponseWriter) *responseRecorder {
	if rec, ok := w.(*responseRecorder); ok {
		return rec
	}
	return &responseRecorder{ResponseWriter: w, status: http.StatusOK}
}

func (rec *responseRecorder) WriteHeader(code int) {
	if !rec.wroteHeader {
		rec.status = code
		rec.wroteHeader = true
	}
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *responseRecorder) Write(b []byte) (int, error) {
	rec.wroteHeader = true
	n, err := rec.ResponseWriter.Write(b)
	rec.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (rec *responseRecorder) Unwrap() http.ResponseWriter {
	return rec.ResponseWriter
}

// accessLog emits one structured log line per request. Health probes are
// logged at debug level: they arrive every few seconds and would drown out
// real traffic, and a 503 from /readyz during shutdown is expected, not an
// error. Failed readiness checks are logged by the health package itself.
func accessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := recordResponse(w)
		next.ServeHTTP(rec, r)

		level := slog.LevelInfo
		switch {
		case r.URL.Path == "/livez" || r.URL.Path == "/readyz":
			level = slog.LevelDebug
		case rec.status >= http.StatusInternalServerError:
			level = slog.LevelError
		}

		logger.LogAttrs(r.Context(), level, "http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Int("bytes", rec.bytes),
			slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
			slog.String("remote_addr", r.RemoteAddr),
			slog.String("user_agent", r.UserAgent()),
		)
	})
}

// recoverPanic turns a handler panic into a 500 problem response instead of
// a dropped connection, and logs the stack trace.
func recoverPanic(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := recordResponse(w)
		defer func() {
			if v := recover(); v != nil {
				handlePanic(logger, rec, r, v)
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

func handlePanic(logger *slog.Logger, rec *responseRecorder, r *http.Request, v any) {
	if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
		panic(v) // deliberate abort: let net/http handle it
	}
	logger.ErrorContext(r.Context(), "panic recovered",
		slog.Any("panic", v),
		slog.String("stack", string(debug.Stack())),
	)
	if !rec.wroteHeader {
		writeProblem(rec, r, problem{
			Status: http.StatusInternalServerError,
			Detail: "An unexpected error occurred.",
		})
	}
}

// requestTimeout bounds the work done for a single request. Handlers and
// repositories observe it through r.Context().
func requestTimeout(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
