//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// seedDownWithAlert puts a monitor into the down state with a recorded down
// event and a matching pending outbox alert, the exact shape the scheduler/ingest
// paths leave for the alerter. It returns the monitor id and the alert id.
func (e *checkinTestEnv) seedDownWithAlert(t *testing.T, downAt time.Time) (pgtype.UUID, int64) {
	t.Helper()
	ctx := context.Background()
	m := e.seedMonitor(t, "down")

	if _, err := e.pool.Exec(ctx,
		`UPDATE monitors SET last_checkin_at=$1, next_deadline=NULL WHERE id=$2`,
		downAt, m.ID); err != nil {
		t.Fatalf("arm down monitor: %v", err)
	}

	var eventID int64
	if err := e.pool.QueryRow(ctx,
		`INSERT INTO events (monitor_id, type, message, created_at)
		 VALUES ($1,'down','Monitor missed its deadline',$2) RETURNING id`,
		m.ID, downAt).Scan(&eventID); err != nil {
		t.Fatalf("seed down event: %v", err)
	}

	var alertID int64
	if err := e.pool.QueryRow(ctx,
		`INSERT INTO alerts (monitor_id, event_id, channel, created_at, next_attempt_at)
		 VALUES ($1,$2,'default',$3,$3) RETURNING id`,
		m.ID, eventID, downAt).Scan(&alertID); err != nil {
		t.Fatalf("seed pending alert: %v", err)
	}
	return m.ID, alertID
}

func (e *checkinTestEnv) alertStatus(t *testing.T, id int64) (status string, attempts int) {
	t.Helper()
	if err := e.pool.QueryRow(context.Background(),
		`SELECT status, attempts FROM alerts WHERE id=$1`, id).Scan(&status, &attempts); err != nil {
		t.Fatalf("read alert %d: %v", id, err)
	}
	return status, attempts
}

// TestClaimDueAlerts_ReturnsJoinContext claims a due alert and asserts the join
// carries the recipient, monitor identity, and event type the worker needs.
func TestClaimDueAlerts_ReturnsJoinContext(t *testing.T) {
	e := newCheckinTestEnv(t)
	e.resetMonitors(t)
	mID, alertID := e.seedDownWithAlert(t, time.Now().UTC().Add(-time.Minute))

	jobs, err := e.store.ClaimDueAlerts(context.Background(), time.Now().UTC(), 100)
	if err != nil {
		t.Fatalf("ClaimDueAlerts: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("claimed %d jobs, want 1", len(jobs))
	}
	j := jobs[0]
	if j.ID != alertID || j.MonitorID != mID.String() {
		t.Errorf("ids = %d/%s, want %d/%s", j.ID, j.MonitorID, alertID, mID.String())
	}
	if j.EventType != "down" || j.Recipient == "" || j.MonitorSlug == "" {
		t.Errorf("job missing context: %+v", j)
	}
}

// TestAlertRetryThenSucceed mirrors AC-1: two retryable failures reschedule the
// alert (staying pending, attempts climbing), the third try succeeds and marks
// it sent with the correct attempt count.
func TestAlertRetryThenSucceed(t *testing.T) {
	e := newCheckinTestEnv(t)
	e.resetMonitors(t)
	ctx := context.Background()
	_, alertID := e.seedDownWithAlert(t, time.Now().UTC().Add(-time.Minute))

	// Two retryable failures.
	if err := e.store.RescheduleAlert(ctx, alertID, time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatalf("reschedule 1: %v", err)
	}
	if err := e.store.RescheduleAlert(ctx, alertID, time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatalf("reschedule 2: %v", err)
	}
	if status, attempts := e.alertStatus(t, alertID); status != "pending" || attempts != 2 {
		t.Fatalf("after 2 retries: status=%s attempts=%d, want pending/2", status, attempts)
	}

	// Third try succeeds.
	if err := e.store.MarkAlertSent(ctx, alertID); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	status, attempts := e.alertStatus(t, alertID)
	if status != "sent" || attempts != 3 {
		t.Errorf("after success: status=%s attempts=%d, want sent/3", status, attempts)
	}
	// A sent alert is no longer due.
	jobs, _ := e.store.ClaimDueAlerts(ctx, time.Now().UTC(), 100)
	if len(jobs) != 0 {
		t.Errorf("claimed %d after sent, want 0", len(jobs))
	}
}

// TestFailAlertRecordsEvent mirrors AC-1's failure tail: after the retry budget
// is spent, FailAlert marks the row failed AND writes a visible alert_failed
// event.
func TestFailAlertRecordsEvent(t *testing.T) {
	e := newCheckinTestEnv(t)
	e.resetMonitors(t)
	ctx := context.Background()
	mID, alertID := e.seedDownWithAlert(t, time.Now().UTC().Add(-time.Minute))

	if err := e.store.FailAlert(ctx, alertID, mID.String(), "mail server rejected the message"); err != nil {
		t.Fatalf("FailAlert: %v", err)
	}
	if status, _ := e.alertStatus(t, alertID); status != "failed" {
		t.Errorf("status = %s, want failed", status)
	}
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1 AND type='alert_failed'`, mID); n != 1 {
		t.Errorf("alert_failed events = %d, want 1", n)
	}
}

// TestSuppressAlert mirrors AC-3: a muted monitor's alert is resolved without a
// send — it leaves the pending outbox and keeps sent_at NULL.
func TestSuppressAlert(t *testing.T) {
	e := newCheckinTestEnv(t)
	e.resetMonitors(t)
	ctx := context.Background()
	_, alertID := e.seedDownWithAlert(t, time.Now().UTC().Add(-time.Minute))

	if err := e.store.SuppressAlert(ctx, alertID); err != nil {
		t.Fatalf("SuppressAlert: %v", err)
	}
	if status, _ := e.alertStatus(t, alertID); status != "sent" {
		t.Errorf("status = %s, want sent (resolved)", status)
	}
	if n := e.countRows(t, `SELECT count(*) FROM alerts WHERE id=$1 AND sent_at IS NULL`, alertID); n != 1 {
		t.Errorf("suppressed alert should keep sent_at NULL")
	}
	jobs, _ := e.store.ClaimDueAlerts(ctx, time.Now().UTC(), 100)
	if len(jobs) != 0 {
		t.Errorf("suppressed alert still claimable: %d", len(jobs))
	}
}

// TestResolveDowntime returns the interval from the down event to recovery.
func TestResolveDowntime(t *testing.T) {
	e := newCheckinTestEnv(t)
	e.resetMonitors(t)
	downAt := time.Now().UTC().Add(-42 * time.Minute)
	mID, _ := e.seedDownWithAlert(t, downAt)

	d, err := e.store.ResolveDowntime(context.Background(), mID.String(), downAt.Add(42*time.Minute))
	if err != nil {
		t.Fatalf("ResolveDowntime: %v", err)
	}
	if d < 41*time.Minute || d > 43*time.Minute {
		t.Errorf("downtime = %v, want ~42m", d)
	}
}

// TestEnqueueDownReminders mirrors AC-3's reminder rules: a reminder is queued
// once the cadence elapses for a down monitor, but never while muted.
func TestEnqueueDownReminders(t *testing.T) {
	e := newCheckinTestEnv(t)
	e.resetMonitors(t)
	ctx := context.Background()
	// Down 2h ago; its only alert (the down alert) is 2h old too.
	mID, _ := e.seedDownWithAlert(t, time.Now().UTC().Add(-2*time.Hour))
	// 1h reminder cadence.
	if _, err := e.pool.Exec(ctx, `UPDATE monitors SET reminder_every_s=3600 WHERE id=$1`, mID); err != nil {
		t.Fatalf("set cadence: %v", err)
	}

	n, err := e.store.EnqueueDownReminders(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("EnqueueDownReminders: %v", err)
	}
	if n != 1 {
		t.Fatalf("queued %d reminders, want 1", n)
	}
	if got := e.countRows(t, `SELECT count(*) FROM alerts WHERE monitor_id=$1 AND status='pending'`, mID); got != 2 {
		t.Errorf("pending alerts = %d, want 2 (down + reminder)", got)
	}
	// The reminder row is flagged is_reminder=true (the down alert is not), so
	// the alerter renders the "still down" template rather than the down one.
	if got := e.countRows(t, `SELECT count(*) FROM alerts WHERE monitor_id=$1 AND is_reminder=true`, mID); got != 1 {
		t.Errorf("is_reminder alerts = %d, want exactly 1 (the reminder)", got)
	}

	// Running again immediately queues nothing (a pending reminder already exists
	// and the newest alert is within the cadence window).
	n2, err := e.store.EnqueueDownReminders(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("EnqueueDownReminders 2: %v", err)
	}
	if n2 != 0 {
		t.Errorf("second run queued %d reminders, want 0", n2)
	}

	// Muting suppresses reminders even after clearing the pending one and aging
	// the alerts past the cadence.
	if _, err := e.pool.Exec(ctx,
		`UPDATE monitors SET alerts_muted=true WHERE id=$1`, mID); err != nil {
		t.Fatalf("mute: %v", err)
	}
	if _, err := e.pool.Exec(ctx,
		`UPDATE alerts SET status='sent', created_at=now()-interval '2 hours' WHERE monitor_id=$1`, mID); err != nil {
		t.Fatalf("age alerts: %v", err)
	}
	n3, err := e.store.EnqueueDownReminders(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("EnqueueDownReminders 3: %v", err)
	}
	if n3 != 0 {
		t.Errorf("muted monitor queued %d reminders, want 0", n3)
	}
}
