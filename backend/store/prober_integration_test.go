//go:build integration

package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sukhera/ping/db"
)

// seedHTTPMonitor creates a user and an http monitor with the given initial
// state/fail_streak/fail_threshold, for prober confirmation-threshold tests.
func (e *checkinTestEnv) seedHTTPMonitor(t *testing.T, state string, failStreak, failThreshold int32) db.Monitor {
	t.Helper()
	ctx := context.Background()

	email := "prober+" + strings.ReplaceAll(t.Name(), "/", "_") + time.Now().Format("150405.000000000") + "@example.test"
	user, err := e.q.CreateUser(ctx, db.CreateUserParams{Email: email, PasswordHash: "x"})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	m, err := e.q.CreateMonitor(ctx, db.CreateMonitorParams{
		UserID:        user.ID,
		Kind:          "http",
		Slug:          "prober-" + time.Now().Format("150405.000000000"),
		Name:          "prober monitor",
		Tz:            "UTC",
		Url:           pgtype.Text{String: "https://example.test", Valid: true},
		Method:        pgtype.Text{String: "GET", Valid: true},
		IntervalS:     pgtype.Int4{Int32: 60, Valid: true},
		TimeoutS:      pgtype.Int4{Int32: 10, Valid: true},
		FailThreshold: pgtype.Int4{Int32: failThreshold, Valid: true},
	})
	if err != nil {
		t.Fatalf("seed monitor: %v", err)
	}

	if _, err := e.pool.Exec(ctx,
		`UPDATE monitors SET state=$1, fail_streak=$2 WHERE id=$3`,
		state, failStreak, m.ID); err != nil {
		t.Fatalf("arm monitor: %v", err)
	}
	m.State = state
	m.FailStreak = failStreak
	return m
}

func failOutcome(monitorID string, priorState string, priorFailStreak, failThreshold int32, errMsg string) ProbeOutcome {
	return ProbeOutcome{
		MonitorID:       monitorID,
		OK:              false,
		Error:           errMsg,
		FailThreshold:   failThreshold,
		PriorState:      priorState,
		PriorFailStreak: priorFailStreak,
		NextProbeAt:     time.Now().Add(time.Minute),
	}
}

func successOutcome(monitorID string, priorState string, priorFailStreak int32) ProbeOutcome {
	status := int32(200)
	latency := int32(42)
	return ProbeOutcome{
		MonitorID:       monitorID,
		OK:              true,
		HTTPStatus:      &status,
		LatencyMS:       &latency,
		PriorState:      priorState,
		PriorFailStreak: priorFailStreak,
		NextProbeAt:     time.Now().Add(time.Minute),
	}
}

// TestRecordProbeResult_ConfirmationThreshold walks a fail_threshold=2 http
// monitor through a single blip (no transition), a second consecutive failure
// (down + exactly one alert), and a recovering success (up + one recovery
// alert) — PING-017 AC.
func TestRecordProbeResult_ConfirmationThreshold(t *testing.T) {
	e := newCheckinTestEnv(t)
	m := e.seedHTTPMonitor(t, "up", 0, 2)
	ctx := context.Background()

	// First failure: streak 0→1, below threshold — no transition.
	res, err := e.store.RecordProbeResult(ctx, failOutcome(m.ID.String(), "up", 0, 2, "request timed out"))
	if err != nil {
		t.Fatalf("RecordProbeResult (1st failure): %v", err)
	}
	if res.Transition != "" {
		t.Errorf("transition = %q after 1st failure, want none (blip absorbed)", res.Transition)
	}
	got := e.getMonitor(t, m.ID)
	if got.State != "up" {
		t.Errorf("state = %q after 1st failure, want up", got.State)
	}
	if got.FailStreak != 1 {
		t.Errorf("fail_streak = %d after 1st failure, want 1", got.FailStreak)
	}

	// Second consecutive failure: streak 1→2, reaches threshold — down + alert.
	res, err = e.store.RecordProbeResult(ctx, failOutcome(m.ID.String(), "up", 1, 2, "unexpected status 500"))
	if err != nil {
		t.Fatalf("RecordProbeResult (2nd failure): %v", err)
	}
	if res.Transition != "down" {
		t.Errorf("transition = %q after 2nd failure, want down", res.Transition)
	}
	got = e.getMonitor(t, m.ID)
	if got.State != "down" {
		t.Errorf("state = %q after 2nd failure, want down", got.State)
	}
	if n := e.countRows(t, `SELECT count(*) FROM alerts WHERE monitor_id=$1`, m.ID); n != 1 {
		t.Errorf("alerts = %d after down, want exactly 1", n)
	}
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1 AND type='down'`, m.ID); n != 1 {
		t.Errorf("down events = %d, want 1", n)
	}

	// Next success: immediate recovery (1 success required) — up + recovery alert.
	res, err = e.store.RecordProbeResult(ctx, successOutcome(m.ID.String(), "down", 2))
	if err != nil {
		t.Fatalf("RecordProbeResult (recovery): %v", err)
	}
	if res.Transition != "up" {
		t.Errorf("transition = %q after recovery, want up", res.Transition)
	}
	got = e.getMonitor(t, m.ID)
	if got.State != "up" {
		t.Errorf("state = %q after recovery, want up", got.State)
	}
	if got.FailStreak != 0 {
		t.Errorf("fail_streak = %d after recovery, want 0 (reset)", got.FailStreak)
	}
	if n := e.countRows(t, `SELECT count(*) FROM alerts WHERE monitor_id=$1`, m.ID); n != 2 {
		t.Errorf("alerts = %d after recovery, want 2 (down + up)", n)
	}
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1 AND type='up'`, m.ID); n != 1 {
		t.Errorf("up events = %d, want 1", n)
	}
}

// TestRecordProbeResult_RepeatFailureNoDuplicateAlert covers a monitor already
// down that keeps failing: no new event/alert should be written per repeat
// failure (matches applyFail's dedup behavior in the check-in path).
func TestRecordProbeResult_RepeatFailureNoDuplicateAlert(t *testing.T) {
	e := newCheckinTestEnv(t)
	m := e.seedHTTPMonitor(t, "down", 3, 2)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := e.store.RecordProbeResult(ctx, failOutcome(m.ID.String(), "down", 3+int32(i), 2, "request timed out")); err != nil {
			t.Fatalf("RecordProbeResult (repeat failure %d): %v", i, err)
		}
	}

	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1 AND type='down'`, m.ID); n != 0 {
		t.Errorf("down events = %d, want 0 (monitor was already down, no new transition)", n)
	}
	if n := e.countRows(t, `SELECT count(*) FROM alerts WHERE monitor_id=$1`, m.ID); n != 0 {
		t.Errorf("alerts = %d, want 0", n)
	}
	if n := e.countRows(t, `SELECT count(*) FROM probe_results WHERE monitor_id=$1`, m.ID); n != 3 {
		t.Errorf("probe_results rows = %d, want 3 (every attempt still recorded)", n)
	}
}

// TestRecordProbeResult_DistinctErrorStrings asserts each failure mode is
// recorded with its own distinct probe_results.error string, per PING-017 AC.
func TestRecordProbeResult_DistinctErrorStrings(t *testing.T) {
	e := newCheckinTestEnv(t)
	ctx := context.Background()

	messages := []string{
		"request timed out",
		"dns lookup failed",
		"tls handshake failed",
		"unexpected status 500",
		"keyword assertion failed",
	}
	seen := map[string]bool{}

	for _, msg := range messages {
		m := e.seedHTTPMonitor(t, "up", 0, 2)
		if _, err := e.store.RecordProbeResult(ctx, failOutcome(m.ID.String(), "up", 0, 2, msg)); err != nil {
			t.Fatalf("RecordProbeResult(%q): %v", msg, err)
		}
		var stored string
		if err := e.pool.QueryRow(ctx,
			`SELECT error FROM probe_results WHERE monitor_id=$1`, m.ID).Scan(&stored); err != nil {
			t.Fatalf("read probe_results.error: %v", err)
		}
		if stored != msg {
			t.Errorf("stored error = %q, want %q", stored, msg)
		}
		seen[stored] = true
	}

	if len(seen) != len(messages) {
		t.Errorf("distinct stored error strings = %d, want %d", len(seen), len(messages))
	}
}
