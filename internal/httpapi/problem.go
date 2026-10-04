package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/mirza76/todo-service/internal/requestid"
	"github.com/mirza76/todo-service/internal/todo"
)

// problem is an RFC 9457 "Problem Details for HTTP APIs" response body.
// Type is "about:blank", meaning Title is the standard HTTP status phrase.
type problem struct {
	Type     string              `json:"type"`
	Title    string              `json:"title"`
	Status   int                 `json:"status"`
	Detail   string              `json:"detail,omitempty"`
	Instance string              `json:"instance,omitempty"`
	Errors   []fieldErrorPayload `json:"errors,omitempty"`
	// RequestID is an RFC 9457 extension member: clients can quote it when
	// reporting a problem, and it matches the server's log lines.
	RequestID string `json:"request_id,omitempty"`
}

type fieldErrorPayload struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// requestError is a client error detected by the HTTP layer itself (bad
// JSON, wrong content type, oversized body, malformed query parameter).
type requestError struct {
	status int
	detail string
}

func (e *requestError) Error() string { return e.detail }

func badRequest(detail string) *requestError {
	return &requestError{status: http.StatusBadRequest, detail: detail}
}

func writeProblem(w http.ResponseWriter, r *http.Request, p problem) {
	p.Type = "about:blank"
	p.Title = http.StatusText(p.Status)
	p.Instance = r.URL.Path
	p.RequestID = requestid.FromContext(r.Context())
	writeBody(w, p.Status, "application/problem+json", p)
}

// writeError maps an error to the matching HTTP problem response. It is the
// single place where domain and infrastructure errors become status codes.
func writeError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	var (
		reqErr *requestError
		valErr *todo.ValidationError
	)

	switch {
	case errors.As(err, &reqErr):
		writeProblem(w, r, problem{Status: reqErr.status, Detail: reqErr.detail})

	case errors.As(err, &valErr):
		fields := make([]fieldErrorPayload, len(valErr.Fields))
		for i, f := range valErr.Fields {
			fields[i] = fieldErrorPayload{Field: f.Field, Message: f.Message}
		}
		writeProblem(w, r, problem{
			Status: http.StatusUnprocessableEntity,
			Detail: "The request contains invalid fields.",
			Errors: fields,
		})

	case errors.Is(err, todo.ErrNotFound):
		writeProblem(w, r, problem{Status: http.StatusNotFound, Detail: "Todo not found."})

	case errors.Is(err, todo.ErrAlreadyExists):
		writeProblem(w, r, problem{Status: http.StatusConflict, Detail: "Todo already exists."})

	case errors.Is(err, context.DeadlineExceeded):
		logger.WarnContext(r.Context(), "request timed out", slog.Any("error", err))
		writeProblem(w, r, problem{Status: http.StatusServiceUnavailable, Detail: "The request timed out. Please retry."})

	case errors.Is(err, context.Canceled):
		// The client went away; nobody will read the response.
		logger.DebugContext(r.Context(), "request canceled by client", slog.Any("error", err))
		writeProblem(w, r, problem{Status: http.StatusServiceUnavailable, Detail: "The request was canceled."})

	default:
		// Never leak internal details to clients; log them instead.
		logger.ErrorContext(r.Context(), "unhandled error", slog.Any("error", err))
		writeProblem(w, r, problem{Status: http.StatusInternalServerError, Detail: "An unexpected error occurred."})
	}
}
