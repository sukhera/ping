package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sukhera/ping/db"
)

// ClaimDueProbes claims up to limit http monitors whose next_probe_at has
// passed, locking each with FOR UPDATE SKIP LOCKED so a second prober replica
// claims different rows. This is a single statement (no explicit
// multi-statement transaction): pgx wraps it in its own implicit transaction,
// so the row locks are released the instant the statement completes — the
// caller must not hold onto the claim while doing the (slow, external) HTTP
// probe. now is injected for test determinism (pass time.Now() in prod).
func (s *Store) ClaimDueProbes(ctx context.Context, now time.Time, limit int32) ([]Monitor, error) {
	rows, err := s.q.ClaimDueProbes(ctx, db.ClaimDueProbesParams{
		Now:       pgtype.Timestamptz{Time: now, Valid: true},
		PageLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("store: claim due probes: %w", err)
	}
	monitors := make([]Monitor, len(rows))
	for i, row := range rows {
		monitors[i] = toMonitor(row)
	}
	return monitors, nil
}

// ProbeOutcome is what the prober worker observed for one HTTP monitor,
// independent of the monitor's prior state — the store computes the
// confirmation-threshold transition from this plus the monitor's current
// fail_streak/state.
type ProbeOutcome struct {
	MonitorID    string
	OK           bool
	HTTPStatus   *int32
	LatencyMS    *int32
	Error        string // "" on success
	TLSExpiresAt *time.Time
	// FailThreshold is the monitor's configured confirmation threshold
	// (defaults applied by the caller — PRD F2.2 default is 2).
	FailThreshold int32
	// PriorState/PriorFailStreak are the values the claim observed, so the
	// transition logic doesn't need a second read inside the transaction.
	PriorState      string
	PriorFailStreak int32
	// NextProbeAt is now + the monitor's interval_s, computed by the caller.
	NextProbeAt time.Time
}

// RecordProbeResultResult reports what RecordProbeResult did, mirroring
// RecordCheckinResult's shape. Transition is "" for a no-op probe (repeat
// success, or a failure that hasn't yet reached the threshold).
type RecordProbeResultResult struct {
	State      string
	Transition string
}

// RecordProbeResult applies one probe's outcome to its monitor in a single
// transaction: it inserts the probe_results row and updates the monitor's
// fail_streak/state/next_probe_at, writing a timeline event plus a pending
// outbox alert only on an actual up/down transition (never on an
// unconfirmed failure or a repeat success) — the same confirmed-transition
// shape as store/checkin.go's applySuccess/applyFail and
// store/scheduler.go's evaluateOne.
//
// Confirmation logic (PRD F2.2): a failure increments fail_streak but only
// flips state to "down" once fail_streak reaches FailThreshold — a single
// blip is absorbed. Any success resets fail_streak to 0 and flips state to
// "up" immediately (one success confirms recovery).
func (s *Store) RecordProbeResult(ctx context.Context, o ProbeOutcome) (RecordProbeResultResult, error) {
	var result RecordProbeResultResult

	err := s.withTx(ctx, func(q *db.Queries) error {
		monitorID, err := pgUUID(o.MonitorID)
		if err != nil {
			return err
		}

		if _, err := q.InsertProbeResult(ctx, db.InsertProbeResultParams{
			MonitorID:    monitorID,
			Ok:           o.OK,
			HttpStatus:   nullableInt32(o.HTTPStatus),
			LatencyMs:    nullableInt32(o.LatencyMS),
			Error:        textOrNull(o.Error),
			TlsExpiresAt: nullableTimestamptz(o.TLSExpiresAt),
		}); err != nil {
			return fmt.Errorf("store: insert probe result: %w", err)
		}

		newState, newFailStreak := nextProbeState(o)
		nextProbeAt := pgtype.Timestamptz{Time: o.NextProbeAt, Valid: true}

		if err := q.UpdateMonitorOnProbe(ctx, db.UpdateMonitorOnProbeParams{
			State:       newState,
			FailStreak:  newFailStreak,
			NextProbeAt: nextProbeAt,
			ID:          monitorID,
		}); err != nil {
			return fmt.Errorf("store: update monitor on probe: %w", err)
		}

		result.State = newState

		switch {
		case newState == "down" && o.PriorState != "down":
			result.Transition = "down"
			return recordTransition(ctx, q, monitorID, "down", probeFailureMessage(o))
		case newState == "up" && o.PriorState == "down":
			result.Transition = "up"
			return recordTransition(ctx, q, monitorID, "up", "Monitor recovered")
		}
		return nil
	})
	if err != nil {
		return RecordProbeResultResult{}, err
	}
	return result, nil
}

// nextProbeState applies the confirmation-threshold rule to one outcome,
// returning the monitor's new state and fail_streak.
func nextProbeState(o ProbeOutcome) (state string, failStreak int32) {
	if o.OK {
		return "up", 0
	}
	failStreak = o.PriorFailStreak + 1
	threshold := o.FailThreshold
	if threshold <= 0 {
		threshold = 2 // PRD F2.2 default
	}
	if failStreak >= threshold {
		return "down", failStreak
	}
	// Below threshold: state is unchanged (a monitor that was already "up"
	// stays "up" through an unconfirmed blip; PriorState covers "new" too).
	if o.PriorState == "" {
		return "up", failStreak
	}
	return o.PriorState, failStreak
}

// probeFailureMessage renders the down-transition event message from the
// probe's recorded error, falling back to a generic message if none was set.
func probeFailureMessage(o ProbeOutcome) string {
	if o.Error == "" {
		return "Monitor failed its probe"
	}
	return "Monitor failed its probe: " + o.Error
}

func nullableInt32(v *int32) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *v, Valid: true}
}

func nullableTimestamptz(v *time.Time) pgtype.Timestamptz {
	if v == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *v, Valid: true}
}
