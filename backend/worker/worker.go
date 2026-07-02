// Package worker provides the shared tick/jitter/graceful-stop loop the
// background workers (scheduler now; prober and alerter later) run on, plus the
// Redis liveness heartbeat each writes for /health. Workers are DB-driven, not
// timer-driven: each tick claims due work from Postgres, so the loop here only
// paces the claims and never holds per-monitor state.
package worker

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"
)

// TickFunc does one unit of work. A returned error is logged and the loop
// continues — a transient DB blip must not kill the worker. ctx cancellation
// stops the loop.
type TickFunc func(ctx context.Context) error

// Config configures a single worker loop.
type Config struct {
	// Name identifies the worker in logs and its heartbeat key ("scheduler").
	Name string
	// Interval is the base time between ticks (e.g. 15s for the scheduler).
	Interval time.Duration
	// Jitter is the maximum extra delay added to each interval to desynchronize
	// multiple replicas and reduce FOR UPDATE SKIP LOCKED contention. May be 0.
	Jitter time.Duration
	// Tick is the work done each iteration.
	Tick TickFunc
	// HB writes the liveness heartbeat after each tick. May be nil (no
	// heartbeat written — used in tests).
	HB *Heartbeat
}

// Run executes cfg.Tick every cfg.Interval (plus up to cfg.Jitter) until ctx is
// cancelled, then returns nil for a graceful stop. The first tick fires
// immediately. The heartbeat is written after every tick regardless of tick
// outcome — liveness means "the loop is turning", not "the last tick
// succeeded".
func Run(ctx context.Context, cfg Config) error {
	log := slog.With("worker", cfg.Name)
	log.Info("worker started", "interval", cfg.Interval)

	// A timer (reset each iteration) rather than a ticker so the jitter varies
	// per tick and a long tick can't cause ticks to pile up.
	t := time.NewTimer(0)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("worker stopping")
			return nil
		case <-t.C:
			if err := cfg.Tick(ctx); err != nil && ctx.Err() == nil {
				log.Error("tick failed", "error", err)
			}
			cfg.HB.Write(ctx, cfg.Name)
			t.Reset(cfg.Interval + jitter(cfg.Jitter))
		}
	}
}

// jitter returns a random duration in [0, max). Zero max yields zero. This
// only desynchronizes worker replicas to spread SKIP LOCKED contention — it is
// not security-sensitive, so math/rand/v2 is appropriate.
func jitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(max))) //nolint:gosec // non-crypto jitter
}
