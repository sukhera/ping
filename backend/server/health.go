package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

const healthCheckTimeout = 3 * time.Second

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
				components["scheduler"] = workerHeartbeatStatus(ctx, deps.Redis, "scheduler")
				components["prober"] = workerHeartbeatStatus(ctx, deps.Redis, "prober")
				components["alerter"] = workerHeartbeatStatus(ctx, deps.Redis, "alerter")
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

// workerHeartbeatStatus reads the last-seen timestamp a worker writes to
// Redis (worker:heartbeat:<role>, introduced starting PING-009). A missing
// key means that worker hasn't run yet in this environment, not a failure --
// only Postgres being unreachable degrades /health's overall status.
func workerHeartbeatStatus(ctx context.Context, rdb *redis.Client, role string) componentStatus {
	key := fmt.Sprintf("worker:heartbeat:%s", role)
	_, err := rdb.Get(ctx, key).Result()
	switch err {
	case nil:
		return componentStatus{Status: "up"}
	case redis.Nil:
		return componentStatus{Status: "unknown"}
	default:
		return componentStatus{Status: "unknown", Error: err.Error()}
	}
}
