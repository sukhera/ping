package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// storeError, when implemented by an error returned from the store package,
// lets response mapping pick an HTTP status without the server layer knowing
// about store internals.
type storeError interface {
	error
	HTTPStatus() int
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("response: failed to encode JSON body", "error", err)
	}
}

// writeError maps err to an HTTP status and a safe, generic message.
// Internal error details are logged but never sent to the client.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	message := "internal server error"

	if se, ok := errors.AsType[storeError](err); ok {
		status = se.HTTPStatus()
		message = se.Error()
	}

	if status >= http.StatusInternalServerError {
		slog.ErrorContext(r.Context(), "request failed",
			"request_id", requestIDFromContext(r.Context()),
			"error", err,
			"status", status,
		)
	}

	writeJSON(w, status, errorResponse{Error: message})
}
