// Package rollup runs the nightly rollup + retention loop: once a day it
// folds the prior UTC day's raw checkins/probe_results/events into a
// daily_stats row per monitor, then prunes raw rows older than the retention
// window. All the aggregation and deletion logic lives in the store (package
// direction: worker → store); this package owns only the loop, its cadence,
// and deciding which day is next to roll up.
package rollup

import (
	"context"
	"log/slog"
	"time"

	"github.com/sukhera/ping/store"
	"github.com/sukhera/ping/worker"
)

const (
	// DefaultInterval is the production tick cadence. The job itself only
	// needs to run once a day, but ticking hourly means a restart (deploy,
	// crash) never leaves the rollup more than an hour late, and lastRolled
	// makes every tick beyond the first same-day one a cheap no-op.
	DefaultInterval = time.Hour

	// DefaultRetentionDays is used when the operator hasn't set RETENTION_DAYS
	// (PRD F6.4: "pruned after 90 days (configurable)").
	DefaultRetentionDays = 90
)

// Store is the slice of *store.Store the rollup job needs, as an interface so
// the loop can be tested without Postgres.
type Store interface {
	RollupDay(ctx context.Context, day time.Time) (store.RollupResult, error)
	PruneOldData(ctx context.Context, cutoff time.Time) (store.PruneResult, error)
}

// Rollup runs the daily rollup+prune tick, tracking the last UTC day it
// successfully rolled up so repeated ticks within the same day are no-ops.
type Rollup struct {
	st             Store
	retentionDays  int
	lastRolledDate time.Time // zero value: no successful rollup yet this process
}

// New builds a Rollup. retentionDays <= 0 falls back to DefaultRetentionDays
// rather than pruning nothing or everything, since a misconfigured "0" is far
// more likely to be an unset/parsed-wrong value than an intentional
// "retain nothing".
func New(st Store, retentionDays int) *Rollup {
	if retentionDays <= 0 {
		retentionDays = DefaultRetentionDays
	}
	return &Rollup{st: st, retentionDays: retentionDays}
}

// Run starts the rollup loop and blocks until ctx is cancelled (graceful
// stop). interval is a parameter so integration tests can drive fast ticks;
// production passes DefaultInterval.
func Run(ctx context.Context, st Store, hb *worker.Heartbeat, interval time.Duration, retentionDays int) error {
	return New(st, retentionDays).run(ctx, hb, interval)
}

func (r *Rollup) run(ctx context.Context, hb *worker.Heartbeat, interval time.Duration) error {
	return worker.Run(ctx, worker.Config{
		Name:     "rollup",
		Interval: interval,
		Jitter:   interval / 8,
		HB:       hb,
		Tick:     r.tick,
	})
}

// tick rolls up the last fully-complete UTC day (yesterday) if it hasn't
// already been rolled up this process, then prunes raw data past the
// retention window. Rollup and prune are independent operations — a prune
// failure must not skip next tick's rollup and vice versa — so each error is
// logged rather than returned, keeping one bad tick from wedging the loop
// (same "log and continue" shape as scheduler/alerter ticks).
func (r *Rollup) tick(ctx context.Context) error {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	yesterday := today.AddDate(0, 0, -1)

	if !r.lastRolledDate.Equal(yesterday) {
		res, err := r.st.RollupDay(ctx, yesterday)
		if err != nil {
			slog.ErrorContext(ctx, "rollup: roll up day failed", "day", yesterday.Format(time.DateOnly), "error", err)
		} else {
			r.lastRolledDate = yesterday
			slog.InfoContext(ctx, "rollup: day rolled up", "day", yesterday.Format(time.DateOnly), "monitors", res.MonitorsUpdated)
		}
	}

	cutoff := today.AddDate(0, 0, -r.retentionDays)
	pruned, err := r.st.PruneOldData(ctx, cutoff)
	if err != nil {
		slog.ErrorContext(ctx, "rollup: prune failed", "cutoff", cutoff.Format(time.DateOnly), "error", err)
		return nil
	}
	if pruned.Checkins > 0 || pruned.ProbeResults > 0 || pruned.Events > 0 {
		slog.InfoContext(ctx, "rollup: pruned old data",
			"cutoff", cutoff.Format(time.DateOnly),
			"checkins", pruned.Checkins, "probe_results", pruned.ProbeResults, "events", pruned.Events)
	}
	return nil
}
