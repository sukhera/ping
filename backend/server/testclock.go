//go:build e2e

package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sukhera/ping/internal/testclock"
	"github.com/sukhera/ping/store"
	"github.com/sukhera/ping/worker/alerter"
	"github.com/sukhera/ping/worker/prober"
)

// testTickLimit generously covers the handful of monitors any single e2e spec
// creates; it does not need to match the production workers' claimLimit.
const testTickLimit = 500

// maxSchedulerPasses bounds the up->late->down re-evaluation loop below. Each
// EvaluateDueMonitors call advances a monitor by exactly one threshold (its
// next_deadline), matching a real scheduler tick — so crossing both period and
// grace in one time-warp needs two passes. A handful of passes comfortably
// covers any realistic monitor count/threshold chain in an e2e spec.
const maxSchedulerPasses = 10

// registerTestRoutes wires the time-warp endpoint (PING-022) that lets
// Playwright cross real deadlines instantly instead of sleeping in wall-clock
// time. Only reachable when this binary was built with `-tags e2e` (this file)
// AND deps.Env == "test" — the runtime check is defense in depth in case an
// e2e-tagged binary is ever run outside the test environment by mistake.
func registerTestRoutes(r chi.Router, st *store.Store, deps Deps) {
	if deps.Env != "test" {
		return
	}
	h := &testClockHandler{store: st, deps: deps}
	r.Post("/test/advance-clock", h.advance)
}

type testClockHandler struct {
	store *store.Store
	deps  Deps
}

type advanceClockRequest struct {
	Seconds int `json:"seconds"`
}

type advanceClockResponse struct {
	Now time.Time `json:"now"`
}

// advance moves the shared test clock forward and immediately drives one pass
// of every deadline-sensitive worker loop (scheduler, alerter, prober) with
// the new time, rather than relying on a background worker to notice —
// the e2e CI job runs the API only (`--role=api`), so nothing would otherwise
// ever pick up the moved clock there.
func (h *testClockHandler) advance(w http.ResponseWriter, r *http.Request) {
	var req advanceClockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Seconds <= 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "seconds must be a positive integer"})
		return
	}

	now := testclock.Advance(time.Duration(req.Seconds) * time.Second)
	ctx := r.Context()

	// Each pass only advances a monitor across one threshold (up->late or
	// late->down); repeat until a pass claims nothing so a jump spanning both
	// thresholds (or several monitors at different stages) fully settles.
	for range maxSchedulerPasses {
		res, err := h.store.EvaluateDueMonitors(ctx, now, testTickLimit)
		if err != nil {
			slog.ErrorContext(ctx, "test/advance-clock: scheduler pass failed", "error", err)
			writeError(w, r, err)
			return
		}
		if res.Claimed == 0 {
			break
		}
	}

	if err := prober.RunOnce(ctx, h.store, h.deps.SSRFAllowlist); err != nil {
		slog.ErrorContext(ctx, "test/advance-clock: prober pass failed", "error", err)
		writeError(w, r, err)
		return
	}

	if err := alerter.RunOnce(ctx, h.store, h.deps.AlertChannel, h.deps.BaseURL); err != nil {
		slog.ErrorContext(ctx, "test/advance-clock: alerter pass failed", "error", err)
		writeError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, advanceClockResponse{Now: now})
}
