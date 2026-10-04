// Package httpapi is the HTTP delivery layer: routing, request decoding,
// response encoding, error mapping, and middleware. It translates between
// HTTP and the service layer and contains no business rules.
package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/mirza76/todo-service/api"
	"github.com/mirza76/todo-service/internal/metrics"
	"github.com/mirza76/todo-service/internal/requestid"
)

// Config holds the dependencies and settings for the HTTP handler.
type Config struct {
	Service        TodoService
	Logger         *slog.Logger
	Liveness       http.HandlerFunc
	Readiness      http.HandlerFunc
	MaxBodyBytes   int64
	RequestTimeout time.Duration
	// Metrics is optional; when nil, requests are not instrumented.
	Metrics *metrics.HTTP
}

// NewHandler builds the complete HTTP handler with routes and middleware.
func NewHandler(cfg Config) http.Handler {
	h := &todoHandler{svc: cfg.Service, logger: cfg.Logger, maxBodyBytes: cfg.MaxBodyBytes}

	instrument := func(route string, next http.Handler) http.Handler {
		if cfg.Metrics == nil {
			return next
		}
		return cfg.Metrics.Instrument(route, next)
	}
	// Each route is instrumented at registration, labelled with its pattern
	// (e.g. "/todos/{id}") so metric cardinality stays bounded.
	mux := http.NewServeMux()
	handle := func(pattern string, h http.HandlerFunc) {
		_, route, _ := strings.Cut(pattern, " ")
		mux.Handle(pattern, instrument(route, h))
	}
	handle("GET /todos", h.list)
	handle("POST /todos", h.create)
	handle("GET /todos/{id}", h.get)
	handle("PUT /todos/{id}", h.replace)
	handle("PATCH /todos/{id}", h.update)
	handle("DELETE /todos/{id}", h.delete)
	handle("GET /livez", cfg.Liveness)
	handle("GET /readyz", cfg.Readiness)
	handle("GET /openapi.yaml", serveSpec)

	// Middleware is applied inside-out. requestid is outermost so every log
	// line (including the access log) carries the ID; accessLog sees the
	// final status, including 500s produced by recoverPanic.
	handler := problemFallback(mux, instrument)
	handler = requestTimeout(cfg.RequestTimeout, handler)
	handler = recoverPanic(cfg.Logger, handler)
	handler = accessLog(cfg.Logger, handler)
	handler = requestid.Middleware(handler)
	return handler
}

// serveSpec serves the embedded OpenAPI document (media type per RFC 9512).
func serveSpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(api.Spec)
}

// problemFallback makes the mux's built-in 404 and 405 responses use the
// same problem+json format as every other error, keeping the Allow header
// the mux sets for 405. Unmatched requests are instrumented under a single
// "unmatched" route so scanners probing random paths can't create series.
func problemFallback(mux *http.ServeMux, instrument func(string, http.Handler) http.Handler) http.Handler {
	unmatched := instrument("unmatched", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture := &responseCapture{header: w.Header(), status: http.StatusOK}
		mux.ServeHTTP(capture, r)

		switch capture.status {
		case http.StatusNotFound:
			writeProblem(w, r, problem{Status: capture.status, Detail: "The requested resource does not exist."})
		case http.StatusMethodNotAllowed:
			writeProblem(w, r, problem{Status: capture.status, Detail: "The method is not allowed for the requested resource."})
		default:
			// Anything else (e.g. a redirect to a canonical path) passes
			// through unchanged.
			w.WriteHeader(capture.status)
			_, _ = w.Write(capture.body.Bytes())
		}
	}))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		unmatched.ServeHTTP(w, r)
	})
}

// responseCapture buffers the response of the mux's fallback handlers so it
// can be replaced. Headers (e.g. Allow) are shared with the real writer.
type responseCapture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *responseCapture) Header() http.Header         { return c.header }
func (c *responseCapture) Write(b []byte) (int, error) { return c.body.Write(b) }
func (c *responseCapture) WriteHeader(code int)        { c.status = code }
