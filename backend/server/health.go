package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	healthCheckTimeout = 3 * time.Second

	// heartbeatStaleAfter is how long a worker's last heartbeat may age before
	// /health reports it down. A present-but-stale heartbeat means the worker
	// process is wedged and must fail health; see workerHeartbeatStatus.
	heartbeatStaleAfter = 60 * time.Second
)

type componentStatus struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type healthResponse struct {
	Status     string                     `json:"status"`
	Components map[string]componentStatus `json:"components"`
}

func healthHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
		defer cancel()

		components := map[string]componentStatus{}
		overallOK := true

		if deps.DB != nil {
			if err := deps.DB.Ping(ctx); err != nil {
				components["postgres"] = componentStatus{Status: "down", Error: err.Error()}
				overallOK = false
			} else {
				components["postgres"] = componentStatus{Status: "up"}
			}
		}

		// Redis is ephemeral (rate limits, cache, worker lease) — per the
		// database-specialist skill, the app runs degraded but correctly
		// when Redis is down, so it never flips /health to 503.
		if deps.Redis != nil {
			if err := deps.Redis.Ping(ctx).Err(); err != nil {
				components["redis"] = componentStatus{Status: "down", Error: err.Error()}
			} else {
				components["redis"] = componentStatus{Status: "up"}
				for _, role := range []string{"scheduler", "prober", "alerter"} {
					cs, healthy := workerHeartbeatStatus(ctx, deps.Redis, role)
					components[role] = cs
					overallOK = overallOK && healthy
				}
			}
		}

		status := http.StatusOK
		overallStatus := "ok"
		if !overallOK {
			status = http.StatusServiceUnavailable
			overallStatus = "degraded"
		}

		writeJSON(w, status, healthResponse{Status: overallStatus, Components: components})
	}
}

// workerHeartbeatStatus reads the Unix-seconds timestamp a worker writes each
// tick to worker:heartbeat:<role> and reports its liveness, returning whether
// it should count toward /health's overall status:
//
//   - present & fresh (age ≤ heartbeatStaleAfter) → up, healthy
//   - present & stale (age > heartbeatStaleAfter)  → down, NOT healthy → 503
//   - missing (redis.Nil)                          → unknown, healthy
//   - redis/parse error                            → unknown, healthy
//
// A missing key must not 503: it means that worker hasn't run in this
// environment (e.g. a --role=api deployment, or a not-yet-started worker). Only
// a present-but-stale heartbeat — a wedged worker — degrades health. Redis
// errors also don't 503, matching the "Redis is ephemeral" policy above.
func workerHeartbeatStatus(ctx context.Context, rdb *redis.Client, role string) (componentStatus, bool) {
	key := fmt.Sprintf("worker:heartbeat:%s", role)
	raw, err := rdb.Get(ctx, key).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return componentStatus{Status: "unknown"}, true
	case err != nil:
		return componentStatus{Status: "unknown", Error: err.Error()}, true
	}

	ts, perr := strconv.ParseInt(raw, 10, 64)
	if perr != nil {
		return componentStatus{Status: "unknown", Error: "invalid heartbeat value"}, true
	}

	age := time.Since(time.Unix(ts, 0))
	if age > heartbeatStaleAfter {
		return componentStatus{
			Status: "down",
			Error:  fmt.Sprintf("last tick %s ago", age.Truncate(time.Second)),
		}, false
	}
	return componentStatus{Status: "up"}, true
}
