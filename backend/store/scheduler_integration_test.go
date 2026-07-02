//go:build integration

package store

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sukhera/ping/db"
)

// resetMonitors clears all monitors (cascading to checkins/events/alerts) so a
// scheduler test — which evaluates monitors globally, not per-user — starts
// from a known-empty slate regardless of what other integration tests left
// behind. Integration tests own the database and run serially.
func (e *checkinTestEnv) resetMonitors(t *testing.T) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `TRUNCATE monitors CASCADE`); err != nil {
		t.Fatalf("reset monitors: %v", err)
	}
}

// seedHeartbeat creates a user + heartbeat monitor with the given period/grace,
// initial state, and next_deadline, for scheduler tests that need precise
// control over the deadline the checkin helper's seedMonitor doesn't expose.
func (e *checkinTestEnv) seedHeartbeat(t *testing.T, state string, periodS, graceS int32, nextDeadline time.Time) db.Monitor {
	t.Helper()
	ctx := context.Background()

	email := "sched+" + strings.ReplaceAll(t.Name(), "/", "_") + time.Now().Format("150405.000000000") + "@example.test"
	user, err := e.q.CreateUser(ctx, db.CreateUserParams{Email: email, PasswordHash: "x"})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	m, err := e.q.CreateMonitor(ctx, db.CreateMonitorParams{
		UserID:       user.ID,
		Kind:         "heartbeat",
		Slug:         "sched-" + time.Now().Format("150405.000000000"),
		Name:         "sched monitor",
		ScheduleKind: pgtype.Text{String: "period", Valid: true},
		PeriodS:      pgtype.Int4{Int32: periodS, Valid: true},
		GraceS:       pgtype.Int4{Int32: graceS, Valid: true},
		Tz:           "UTC",
	})
	if err != nil {
		t.Fatalf("seed monitor: %v", err)
	}

	if _, err := e.pool.Exec(ctx,
		`UPDATE monitors SET state=$1, next_deadline=$2 WHERE id=$3`,
		state, nextDeadline, m.ID); err != nil {
		t.Fatalf("arm monitor: %v", err)
	}
	return e.getMonitor(t, m.ID)
}

// TestEvaluateDueMonitors_UpLateDown walks a 1s-period / 1s-grace monitor
// up→late→down across a sequence of injected ticks, asserting the correct
// events and exactly ONE down alert after 10 ticks (AC-1).
func TestEvaluateDueMonitors_UpLateDown(t *testing.T) {
	e := newCheckinTestEnv(t)
	e.resetMonitors(t)
	t0 := time.Now().UTC().Truncate(time.Second)
	// next_deadline = occurrence (late threshold) = t0 + 1s (period).
	m := e.seedHeartbeat(t, "up", 1, 1, t0.Add(1*time.Second))
	ctx := context.Background()

	eval := func(now time.Time) EvalResult {
		res, err := e.store.EvaluateDueMonitors(ctx, now, 100)
		if err != nil {
			t.Fatalf("EvaluateDueMonitors(%s): %v", now.Sub(t0), err)
		}
		return res
	}

	// Before the late threshold: nothing due.
	if res := eval(t0.Add(500 * time.Millisecond)); res.ToLate != 0 || res.ToDown != 0 {
		t.Fatalf("early tick transitioned: %+v", res)
	}

	// Past the late threshold (t0+1s), before down (t0+2s): up→late.
	if res := eval(t0.Add(1200 * time.Millisecond)); res.ToLate != 1 || res.ToDown != 0 {
		t.Fatalf("late tick = %+v, want ToLate=1", res)
	}
	got := e.getMonitor(t, m.ID)
	if got.State != "late" {
		t.Errorf("state = %q, want late", got.State)
	}
	// next_deadline re-armed to the down threshold: occurrence + grace = t0+2s.
	if wantDown := t0.Add(2 * time.Second); !got.NextDeadline.Valid || !got.NextDeadline.Time.Equal(wantDown) {
		t.Errorf("next_deadline = %v, want down threshold %v", got.NextDeadline.Time, wantDown)
	}

	// Re-claim while still late & before down threshold: no-op, no duplicate late event.
	if res := eval(t0.Add(1500 * time.Millisecond)); res.ToLate != 0 || res.ToDown != 0 {
		t.Fatalf("re-claim before down transitioned: %+v", res)
	}

	// Past the down threshold (t0+2s): late→down.
	if res := eval(t0.Add(2500 * time.Millisecond)); res.ToDown != 1 {
		t.Fatalf("down tick = %+v, want ToDown=1", res)
	}
	got = e.getMonitor(t, m.ID)
	if got.State != "down" {
		t.Errorf("state = %q, want down", got.State)
	}
	if got.NextDeadline.Valid {
		t.Errorf("next_deadline = %v, want NULL on down", got.NextDeadline.Time)
	}
	if got.FailStreak != 1 {
		t.Errorf("fail_streak = %d, want 1", got.FailStreak)
	}

	// Six more ticks (10 total) with the monitor down: nothing more happens.
	for i := 0; i < 6; i++ {
		if res := eval(t0.Add(time.Duration(3+i) * time.Second)); res.Claimed != 0 {
			t.Fatalf("tick %d claimed a down monitor: %+v", i, res)
		}
	}

	// Exactly one late event, one down event, one pending alert.
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1 AND type='late'`, m.ID); n != 1 {
		t.Errorf("late events = %d, want 1", n)
	}
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1 AND type='down'`, m.ID); n != 1 {
		t.Errorf("down events = %d, want 1", n)
	}
	if n := e.countRows(t, `SELECT count(*) FROM alerts WHERE monitor_id=$1`, m.ID); n != 1 {
		t.Errorf("alerts = %d, want exactly 1 (the down alert)", n)
	}
	if n := e.countRows(t, `SELECT count(*) FROM alerts WHERE monitor_id=$1 AND status='pending' AND channel='default'`, m.ID); n != 1 {
		t.Errorf("pending down alerts = %d, want 1", n)
	}
}

// TestEvaluateDueMonitors_ConcurrentNoDuplicates runs two evaluators
// concurrently over a batch of down-due monitors and asserts SKIP LOCKED
// prevents any monitor being transitioned (and alerted) twice (AC-2).
func TestEvaluateDueMonitors_ConcurrentNoDuplicates(t *testing.T) {
	e := newCheckinTestEnv(t)
	e.resetMonitors(t)
	ctx := context.Background()
	const n = 20
	past := time.Now().UTC().Add(-1 * time.Second)

	ids := make([]pgtype.UUID, n)
	for i := range ids {
		// state=late, next_deadline in the past → each is down-due this tick.
		m := e.seedHeartbeat(t, "late", 60, 60, past)
		ids[i] = m.ID
	}

	now := time.Now().UTC()
	var wg sync.WaitGroup
	results := make([]EvalResult, 2)
	errs := make([]error, 2)
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = e.store.EvaluateDueMonitors(ctx, now, n)
		}(w)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("evaluator %d: %v", i, err)
		}
	}

	totalDown := results[0].ToDown + results[1].ToDown
	if totalDown != n {
		t.Errorf("total ToDown across evaluators = %d, want %d (each monitor exactly once)", totalDown, n)
	}

	// Aggregate DB state: every monitor down, exactly one down event + one alert each.
	downMonitors := e.countRows(t, `SELECT count(*) FROM monitors WHERE id = ANY($1) AND state='down'`, ids)
	if downMonitors != n {
		t.Errorf("down monitors = %d, want %d", downMonitors, n)
	}
	downEvents := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id = ANY($1) AND type='down'`, ids)
	if downEvents != n {
		t.Errorf("down events = %d, want %d (no duplicates)", downEvents, n)
	}
	alerts := e.countRows(t, `SELECT count(*) FROM alerts WHERE monitor_id = ANY($1)`, ids)
	if alerts != n {
		t.Errorf("alerts = %d, want %d (no duplicates)", alerts, n)
	}
}

// TestEvaluateDueMonitors_CrashMidTickLeavesNoTrace models a kill -9 before
// commit as an explicitly rolled-back transaction: the transition must vanish
// entirely (no lost deadline, no partial event/alert), and a subsequent normal
// evaluation must then produce exactly one down transition (AC-3).
func TestEvaluateDueMonitors_CrashMidTickLeavesNoTrace(t *testing.T) {
	e := newCheckinTestEnv(t)
	e.resetMonitors(t)
	ctx := context.Background()
	past := time.Now().UTC().Add(-1 * time.Second)
	m := e.seedHeartbeat(t, "late", 60, 60, past) // down-due

	// Begin a tx, do the same work EvaluateDueMonitors would, then ROLLBACK
	// (models a process killed before commit).
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	q := db.New(tx)
	rows, err := q.ClaimDueMonitors(ctx, db.ClaimDueMonitorsParams{
		Now:       pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		PageLimit: 100,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected the down-due monitor to be claimed")
	}
	if err := q.MarkMonitorDown(ctx, m.ID); err != nil {
		t.Fatalf("mark down: %v", err)
	}
	if err := recordTransition(ctx, q, m.ID, "down", "Monitor missed its deadline"); err != nil {
		t.Fatalf("record transition: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// The rolled-back work left no trace: deadline intact, state unchanged, no rows.
	// Compare against the seeded monitor's stored deadline (m came from
	// getMonitor, so it's already at Postgres's microsecond precision) rather
	// than the raw `past` value, whose sub-microsecond nanos don't round-trip.
	got := e.getMonitor(t, m.ID)
	if got.State != "late" {
		t.Errorf("state = %q, want late (transition rolled back)", got.State)
	}
	if !got.NextDeadline.Valid || !got.NextDeadline.Time.Equal(m.NextDeadline.Time) {
		t.Errorf("next_deadline = %v, want intact %v (deadline not lost)", got.NextDeadline.Time, m.NextDeadline.Time)
	}
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1`, m.ID); n != 0 {
		t.Errorf("events = %d, want 0 after rollback", n)
	}
	if n := e.countRows(t, `SELECT count(*) FROM alerts WHERE monitor_id=$1`, m.ID); n != 0 {
		t.Errorf("alerts = %d, want 0 after rollback", n)
	}

	// "Restart": a normal evaluation re-claims the still-due monitor and
	// produces exactly one down transition — no lost deadline, no duplicate.
	res, err := e.store.EvaluateDueMonitors(ctx, time.Now().UTC(), 100)
	if err != nil {
		t.Fatalf("post-restart evaluate: %v", err)
	}
	if res.ToDown != 1 {
		t.Errorf("ToDown = %d, want 1 after restart", res.ToDown)
	}
	if n := e.countRows(t, `SELECT count(*) FROM alerts WHERE monitor_id=$1`, m.ID); n != 1 {
		t.Errorf("alerts = %d, want exactly 1", n)
	}
}
