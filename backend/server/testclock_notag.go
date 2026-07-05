//go:build !e2e

package server

import (
	"github.com/go-chi/chi/v5"

	"github.com/sukhera/ping/store"
)

// registerTestRoutes is a no-op in every binary not built with `-tags e2e` —
// see testclock_e2e.go for the real implementation and PING-022's AC that the
// time-warp endpoint is entirely absent from production builds.
func registerTestRoutes(r chi.Router, st *store.Store, deps Deps) {}
