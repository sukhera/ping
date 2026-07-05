//go:build e2e

package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sukhera/ping/server"
)

// TestAdvanceClock_AbsentOutsideTestEnv asserts the runtime half of PING-022's
// gating: even in a binary built with `-tags e2e`, the endpoint is only
// registered when Deps.Env == "test" — a defense-in-depth belt-and-suspenders
// check alongside the build tag itself (see TestAdvanceClock_AbsentWithoutE2ETag
// in testclock_notag_test.go for the build-tag half).
func TestAdvanceClock_AbsentOutsideTestEnv(t *testing.T) {
	for _, env := range []string{"development", "production", ""} {
		srv := server.New(":0", server.Deps{Env: env})

		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/test/advance-clock", nil)
		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("env=%q: status = %d, want %d (endpoint must not exist outside PING_ENV=test)",
				env, rec.Code, http.StatusNotFound)
		}
	}
}
