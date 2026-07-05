//go:build !e2e

package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sukhera/ping/server"
)

// TestAdvanceClock_AbsentWithoutE2ETag asserts PING-022's AC that the
// time-warp endpoint is compiled out of any binary not built with `-tags e2e`
// — this test file itself has no build tag, so it runs as part of the normal
// (production-shaped) build and must see a 404, not just an auth failure.
func TestAdvanceClock_AbsentWithoutE2ETag(t *testing.T) {
	srv := server.New(":0", server.Deps{Env: "test"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/test/advance-clock", nil)
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (endpoint must not exist in a non-e2e build)", rec.Code, http.StatusNotFound)
	}
}
