// Package scheduler runs the deadline-evaluation loop: every tick it asks the
// store to transition heartbeat monitors whose deadline has passed
// (up→late→down) and writes a liveness heartbeat for /health. All the
// transactional transition logic lives in the store (package direction:
// worker → store); this package owns only the loop and its wiring.
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/sukhera/ping/internal/testclock"
	"github.com/sukhera/ping/store"
	"github.com/sukhera/ping/worker"
)

const (
	// DefaultInterval is the production tick cadence (TECH-PLAN §2.1). A missed
	// deadline is detected within one interval, satisfying the 60s detection NFR.
	DefaultInterval = 15 * time.Second

	// claimLimit caps monitors processed per tick so one tick's transaction
	// stays bounded; the next tick picks up any remainder.
	claimLimit = 500
)

// Store is the slice of *store.Store the scheduler needs, as an interface so
// the loop can be tested without Postgres.
type Store interface {
	EvaluateDueMonitors(ctx context.Context, now time.Time, limit int32) (store.EvalResult, error)
}

// Run starts the scheduler loop and blocks until ctx is cancelled (graceful
// stop). interval is a parameter so integration tests can drive fast ticks;
// production passes DefaultInterval.
func Run(ctx context.Context, st Store, hb *worker.Heartbeat, interval time.Duration) error {
	return worker.Run(ctx, worker.Config{
		Name:     "scheduler",
		Interval: interval,
		// Jitter is a fraction of the interval so replicas desynchronize
		// without materially changing detection latency (e.g. ~1.9s on the 15s
		// default). Proportional so fast test intervals stay fast.
		Jitter: interval / 8,
		HB:     hb,
		Tick: func(ctx context.Context) error {
			res, err := st.EvaluateDueMonitors(ctx, testclock.Now(), claimLimit)
			if err != nil {
				return err
			}
			if res.ToLate > 0 || res.ToDown > 0 {
				slog.InfoContext(ctx, "scheduler transitions",
					"claimed", res.Claimed, "late", res.ToLate, "down", res.ToDown)
			}
			return nil
		},
	})
}
