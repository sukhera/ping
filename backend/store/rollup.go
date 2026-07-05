package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sukhera/ping/db"
)

// pruneBatchLimit caps rows removed per DELETE statement (TECH-PLAN PING-020
// AC: "batches <= 5000 rows, no long lock"). It is a package const rather
// than a RollupDay/PruneOldData parameter because there is no scenario where
// a caller needs a different value — only tests exercise a smaller batch to
// keep fixtures small, and they do that by calling the query package
// directly (see rollup_integration_test.go), not by threading a parameter
// through the public API.
const pruneBatchLimit = 5000

// RollupResult reports how many monitors got a daily_stats row written for
// the rolled-up day, for the worker to log.
type RollupResult struct {
	MonitorsUpdated int
}

// RollupDay computes and upserts daily_stats for one UTC calendar day across
// every monitor with activity that day. day may be any time within the
// target day — it is normalized to that day's UTC midnight boundaries
// internally, so the caller does not need to pre-truncate.
//
// Three independent aggregates are combined per monitor: heartbeat checkin
// counts (RollupCheckinCounts), HTTP probe counts + latency percentiles
// (RollupProbeStats), and downtime seconds from state-transition events
// (RollupDowntimeSeconds, which alone can produce a row for a monitor with no
// checkins/probes that day — e.g. a heartbeat monitor that stayed down
// through a day it received no pings). The three are merged in Go rather than
// one large SQL UNION because a monitor is heartbeat-or-http (never both), so
// at most one of checkins/probes ever has a row for a given monitor — merging
// keeps each query in its own table's index and readable in isolation.
//
// Upserting via UpsertDailyStat's ON CONFLICT DO UPDATE makes this
// idempotent: re-running for the same day recomputes and replaces rather than
// accumulating (TECH-PLAN PING-020 AC-1).
func (s *Store) RollupDay(ctx context.Context, day time.Time) (RollupResult, error) {
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24 * time.Hour)
	pgDay := pgtype.Date{Time: dayStart, Valid: true}
	pgStart := pgtype.Timestamptz{Time: dayStart, Valid: true}
	pgEnd := pgtype.Timestamptz{Time: dayEnd, Valid: true}

	type stat struct {
		checkins, failures     int32
		latencyP50, latencyP95 pgtype.Int4
		downtimeS              int32
	}
	byMonitor := make(map[pgtype.UUID]*stat)
	get := func(id pgtype.UUID) *stat {
		if st, ok := byMonitor[id]; ok {
			return st
		}
		st := &stat{}
		byMonitor[id] = st
		return st
	}

	checkinRows, err := s.q.RollupCheckinCounts(ctx, db.RollupCheckinCountsParams{DayStart: pgStart, DayEnd: pgEnd})
	if err != nil {
		return RollupResult{}, fmt.Errorf("store: rollup checkin counts: %w", err)
	}
	for _, r := range checkinRows {
		st := get(r.MonitorID)
		st.checkins = r.Checkins
		st.failures = r.Failures
	}

	probeRows, err := s.q.RollupProbeStats(ctx, db.RollupProbeStatsParams{DayStart: pgStart, DayEnd: pgEnd})
	if err != nil {
		return RollupResult{}, fmt.Errorf("store: rollup probe stats: %w", err)
	}
	for _, r := range probeRows {
		st := get(r.MonitorID)
		st.checkins = r.Checkins
		st.failures = r.Failures
		st.latencyP50 = r.LatencyP50
		st.latencyP95 = r.LatencyP95
	}

	downtimeRows, err := s.q.RollupDowntimeSeconds(ctx, db.RollupDowntimeSecondsParams{DayStart: pgStart, DayEnd: pgEnd})
	if err != nil {
		return RollupResult{}, fmt.Errorf("store: rollup downtime seconds: %w", err)
	}
	for _, r := range downtimeRows {
		get(r.MonitorID).downtimeS = r.DowntimeS
	}

	for monitorID, st := range byMonitor {
		if err := s.q.UpsertDailyStat(ctx, db.UpsertDailyStatParams{
			MonitorID:  monitorID,
			Day:        pgDay,
			Checkins:   st.checkins,
			Failures:   st.failures,
			DowntimeS:  st.downtimeS,
			LatencyP50: st.latencyP50,
			LatencyP95: st.latencyP95,
		}); err != nil {
			return RollupResult{}, fmt.Errorf("store: upsert daily stat: %w", err)
		}
	}

	return RollupResult{MonitorsUpdated: len(byMonitor)}, nil
}

// PruneResult reports how many rows PruneOldData removed from each raw table,
// for the worker to log.
type PruneResult struct {
	Checkins     int64
	ProbeResults int64
	Events       int64
}

// PruneOldData deletes raw checkins/probe_results/events rows older than
// cutoff, in batches of pruneBatchLimit rows per DELETE statement (TECH-PLAN
// PING-020 AC: batched so no single statement holds a long lock on a table
// ingest is concurrently writing to). daily_stats is never touched — it is
// the durable rollup this data has already been folded into, and pruning it
// would defeat the point of "kept indefinitely for long-range charts"
// (PRD F6.4).
//
// Each table is drained in its own loop (delete a batch, repeat until a
// batch returns fewer than pruneBatchLimit rows) rather than one interleaved
// loop across tables, so a single slow table can't stall progress on the
// others and a partial run (e.g. context cancelled mid-prune) still leaves
// each table's own deletes committed rather than rolled back — retention
// pruning has no atomicity requirement across tables, unlike a state
// transition.
func (s *Store) PruneOldData(ctx context.Context, cutoff time.Time) (PruneResult, error) {
	pgCutoff := pgtype.Timestamptz{Time: cutoff, Valid: true}
	var res PruneResult

	checkins, err := drainBatches(ctx, func() (int64, error) {
		return s.q.DeleteOldCheckinsBatch(ctx, db.DeleteOldCheckinsBatchParams{Cutoff: pgCutoff, BatchLimit: pruneBatchLimit})
	})
	if err != nil {
		return PruneResult{}, fmt.Errorf("store: prune checkins: %w", err)
	}
	res.Checkins = checkins

	probeResults, err := drainBatches(ctx, func() (int64, error) {
		return s.q.DeleteOldProbeResultsBatch(ctx, db.DeleteOldProbeResultsBatchParams{Cutoff: pgCutoff, BatchLimit: pruneBatchLimit})
	})
	if err != nil {
		return PruneResult{}, fmt.Errorf("store: prune probe results: %w", err)
	}
	res.ProbeResults = probeResults

	events, err := drainBatches(ctx, func() (int64, error) {
		return s.q.DeleteOldEventsBatch(ctx, db.DeleteOldEventsBatchParams{Cutoff: pgCutoff, BatchLimit: pruneBatchLimit})
	})
	if err != nil {
		return PruneResult{}, fmt.Errorf("store: prune events: %w", err)
	}
	res.Events = events

	return res, nil
}

// drainBatches repeatedly calls deleteBatch until it reports fewer than
// pruneBatchLimit rows removed (the signal that the table has no more rows
// older than the cutoff), returning the total removed. ctx cancellation
// between batches stops the loop and returns what was deleted so far as an
// error, so a shutdown mid-prune doesn't retry from zero next tick.
func drainBatches(ctx context.Context, deleteBatch func() (int64, error)) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := deleteBatch()
		if err != nil {
			return total, err
		}
		total += n
		if n < pruneBatchLimit {
			return total, nil
		}
	}
}
