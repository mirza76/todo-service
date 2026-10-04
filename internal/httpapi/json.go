package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"
)

// decodeJSON strictly decodes a single JSON object from the request body
// into dst. It enforces the content type, a body size limit, rejects unknown
// fields and trailing data, and converts decoder errors into client-friendly
// messages.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return &requestError{
			status: http.StatusUnsupportedMediaType,
			detail: "Content-Type must be application/json.",
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return decodeError(err)
		}
		return badRequest("Request body must contain a single JSON object.")
	}
	return nil
}

func decodeError(err error) error {
	var (
		syntaxErr *json.SyntaxError
		typeErr   *json.UnmarshalTypeError
		maxErr    *http.MaxBytesError
	)

	switch {
	case errors.As(err, &maxErr):
		return &requestError{
			status: http.StatusRequestEntityTooLarge,
			detail: fmt.Sprintf("Request body must not exceed %d bytes.", maxErr.Limit),
		}
	case errors.Is(err, io.EOF):
		return badRequest("Request body must not be empty.")
	case errors.As(err, &syntaxErr), errors.Is(err, io.ErrUnexpectedEOF):
		return badRequest("Request body contains malformed JSON.")
	case errors.As(err, &typeErr):
		if typeErr.Field == "" {
			return badRequest("Request body must be a JSON object.")
		}
		return badRequest(fmt.Sprintf("Field %q must be a %s.", typeErr.Field, jsonTypeName(typeErr.Type)))
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		// encoding/json exposes no typed error for unknown fields.
		field := strings.TrimPrefix(err.Error(), "json: unknown field ")
		return badRequest(fmt.Sprintf("Request body contains unknown field %s.", field))
	default:
		return badRequest("Request body is invalid.")
	}
}

func jsonTypeName(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	default:
		return "object"
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	writeBody(w, status, "application/json", v)
}

// writeBody marshals before writing headers, so an encoding failure becomes
// a clean 500 rather than a truncated 200.
func writeBody(w http.ResponseWriter, status int, contentType string, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, `{"title":"Internal Server Error","status":500}`, http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}
