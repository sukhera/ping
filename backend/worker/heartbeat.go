package worker

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// heartbeatTTL is deliberately longer than the /health staleness threshold
// (60s, in server/health.go). A briefly-stalled but alive worker's key must
// survive long enough to be read as "stale" (→ down → 503) rather than
// expiring into "unknown" — which /health treats as a not-yet-started worker
// and does not 503 on.
const heartbeatTTL = 90 * time.Second

// Heartbeat writes a worker's liveness timestamp to Redis for /health to read.
// The zero-value/nil Heartbeat and a nil Redis client are both valid and make
// Write a no-op, mirroring the fail-open posture of store.Allow: losing a
// heartbeat degrades observability but must never crash or block a worker.
type Heartbeat struct {
	rdb *redis.Client
}

// NewHeartbeat returns a Heartbeat backed by rdb. rdb may be nil.
func NewHeartbeat(rdb *redis.Client) *Heartbeat {
	return &Heartbeat{rdb: rdb}
}

// Write records that role's worker is alive as of now, under the key
// worker:heartbeat:<role> (the format server/health.go reads), as Unix seconds
// with a TTL. Failures are swallowed (logged at debug) — see the type comment.
func (h *Heartbeat) Write(ctx context.Context, role string) {
	if h == nil || h.rdb == nil {
		return
	}
	key := "worker:heartbeat:" + role
	val := strconv.FormatInt(time.Now().Unix(), 10)
	if err := h.rdb.Set(ctx, key, val, heartbeatTTL).Err(); err != nil {
		slog.DebugContext(ctx, "heartbeat write failed", "role", role, "error", err)
	}
}
