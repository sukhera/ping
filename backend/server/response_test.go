package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type testStoreError struct {
	msg    string
	status int
}

func (e testStoreError) Error() string   { return e.msg }
func (e testStoreError) HTTPStatus() int { return e.status }

func TestWriteError_MapsStoreError(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)

	writeError(rec, req, testStoreError{msg: "not found", status: http.StatusNotFound})

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	var body errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error != "not found" {
		t.Errorf("error = %q, want %q", body.Error, "not found")
	}
}

func TestWriteError_UnmappedErrorHidesDetails(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)

	writeError(rec, req, errors.New("some internal detail: connection refused on 10.0.0.5"))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	var body errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error != "internal server error" {
		t.Errorf("error = %q, want generic message that hides internal details", body.Error)
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()

	writeJSON(rec, http.StatusOK, map[string]string{"hello": "world"})

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}
