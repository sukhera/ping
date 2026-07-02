package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sukhera/ping/db"
)

// EvalResult reports what one scheduler tick did, for the worker to log.
// ToDown equals the number of down events and pending alert rows written.
type EvalResult struct {
	Claimed int
	ToLate  int
	ToDown  int
}

// EvaluateDueMonitors claims heartbeat monitors whose next_deadline has passed
// and walks each one up→late (event, no alert) or late→down (event + one
// pending outbox alert), all in a single transaction: the claim's row locks,
// the state write, the event, and the alert commit atomically. That atomicity
// is the exactly-one-alert guarantee — a crash before commit leaves no trace
// and the monitor stays due for the next tick (no lost deadline, no duplicate).
//
// next_deadline is the "next threshold to act on": for an `up` monitor it is
// the scheduled occurrence (→late); for a `late` monitor it is occurrence +
// grace (→down). The claim's `state IN ('up','late')` filter plus the NULL
// deadline set on →down mean a down monitor is never re-claimed, so re-runs are
// idempotent. now is injected for test determinism (pass time.Now() in prod).
func (s *Store) EvaluateDueMonitors(ctx context.Context, now time.Time, limit int32) (EvalResult, error) {
	var res EvalResult
	err := s.withTx(ctx, func(q *db.Queries) error {
		rows, err := q.ClaimDueMonitors(ctx, db.ClaimDueMonitorsParams{
			Now:       pgtype.Timestamptz{Time: now, Valid: true},
			PageLimit: limit,
		})
		if err != nil {
			return fmt.Errorf("store: claim due monitors: %w", err)
		}
		res.Claimed = len(rows)
		for _, m := range rows {
			if err := evaluateOne(ctx, q, m, &res); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return EvalResult{}, err
	}
	return res, nil
}

// evaluateOne transitions a single claimed monitor. The claim guarantees its
// next_deadline has passed; the current state selects which threshold that was:
// `up` → its occurrence passed → go late (re-arm to occurrence + grace); `late`
// → its down threshold passed → go down.
func evaluateOne(ctx context.Context, q *db.Queries, m db.Monitor, res *EvalResult) error {
	if !m.NextDeadline.Valid {
		// Defensive: the claim predicate (next_deadline < now()) already
		// excludes NULL deadlines, so a claimed row always has one.
		return nil
	}

	switch m.State {
	case "up":
		// up→late: re-arm next_deadline to the down threshold (the occurrence
		// that just passed, plus grace). No alert — lateness is informational.
		grace := time.Duration(m.GraceS.Int32) * time.Second
		downAt := m.NextDeadline.Time.Add(grace)
		if err := q.MarkMonitorLate(ctx, db.MarkMonitorLateParams{
			ID:           m.ID,
			NextDeadline: pgtype.Timestamptz{Time: downAt, Valid: true},
		}); err != nil {
			return fmt.Errorf("store: mark monitor late: %w", err)
		}
		if _, err := recordEvent(ctx, q, m.ID, "late", "Monitor is late"); err != nil {
			return err
		}
		res.ToLate++

	case "late":
		// late→down: clear the deadline, bump the streak, emit the alert. The
		// down monitor drops out of future claims (state filter + NULL deadline).
		if err := q.MarkMonitorDown(ctx, m.ID); err != nil {
			return fmt.Errorf("store: mark monitor down: %w", err)
		}
		if err := recordTransition(ctx, q, m.ID, "down", "Monitor missed its deadline"); err != nil {
			return err
		}
		res.ToDown++
	}
	return nil
}
