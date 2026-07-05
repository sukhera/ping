package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sukhera/ping/db"
)

// AlertJob is one claimed outbox row plus the context the alerter worker needs
// to render and deliver it: the recipient, the monitor's identity/state/mute
// flag, and the triggering event. It is the store's domain shape for a
// db.ClaimDueAlertsRow.
type AlertJob struct {
	ID           int64
	MonitorID    string
	EventID      int64
	Recipient    string
	MonitorName  string
	MonitorSlug  string
	MonitorState string
	AlertsMuted  bool
	// EventType is the triggering event's type ("down", "up"). Reminder rows
	// reference the outage's down event, so IsReminder (not EventType)
	// distinguishes a "still down" reminder from the original down alert.
	EventType string
	// IsReminder is true for a periodic "still down" reminder row.
	IsReminder bool
	EventAt    time.Time
	// TLSExpiresAt is set only for a "tls_expiry" alert, decoded from the
	// triggering event's meta JSON (store/prober.go's tlsExpiryMeta).
	TLSExpiresAt *time.Time
	// Attempts is how many delivery attempts have already been made (0 on the
	// first claim). The worker uses it to pick the backoff step and decide when
	// retries are exhausted.
	Attempts int
}

// ClaimDueAlerts atomically claims up to limit pending outbox rows that are due
// (next_attempt_at in the past), locking each with FOR UPDATE SKIP LOCKED so a
// second replica claims different rows. The transaction commits immediately —
// the lock is NOT held across the (slow, external) SMTP send. This is the
// at-most-once design: after claiming, the worker sends and then marks the row;
// a crash between send and mark drops that one alert rather than risking a
// duplicate (see docs/ARCHITECTURE.md). now is injected for test determinism.
func (s *Store) ClaimDueAlerts(ctx context.Context, now time.Time, limit int32) ([]AlertJob, error) {
	var jobs []AlertJob
	err := s.withTx(ctx, func(q *db.Queries) error {
		rows, err := q.ClaimDueAlerts(ctx, db.ClaimDueAlertsParams{
			Now:       pgtype.Timestamptz{Time: now, Valid: true},
			PageLimit: limit,
		})
		if err != nil {
			return fmt.Errorf("store: claim due alerts: %w", err)
		}
		jobs = make([]AlertJob, 0, len(rows))
		for _, r := range rows {
			jobs = append(jobs, AlertJob{
				ID:           r.ID,
				MonitorID:    r.MonitorID.String(),
				EventID:      r.EventID,
				Recipient:    r.Recipient,
				MonitorName:  r.MonitorName,
				MonitorSlug:  r.MonitorSlug,
				MonitorState: r.MonitorState,
				AlertsMuted:  r.AlertsMuted,
				EventType:    r.EventType,
				IsReminder:   r.IsReminder,
				EventAt:      r.EventCreatedAt.Time,
				Attempts:     int(r.Attempts),
				TLSExpiresAt: decodeTLSExpiresAt(r.EventMeta),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return jobs, nil
}

// MarkAlertSent finalizes a delivered alert.
func (s *Store) MarkAlertSent(ctx context.Context, id int64) error {
	if err := s.q.MarkAlertSent(ctx, id); err != nil {
		return fmt.Errorf("store: mark alert sent: %w", err)
	}
	return nil
}

// SuppressAlert resolves an alert that must not be delivered (its monitor is
// muted) without sending it. The row leaves the pending outbox and is never
// retried; sent_at stays NULL so it is distinguishable from a real delivery.
func (s *Store) SuppressAlert(ctx context.Context, id int64) error {
	if err := s.q.SuppressAlert(ctx, id); err != nil {
		return fmt.Errorf("store: suppress alert: %w", err)
	}
	return nil
}

// RescheduleAlert backs a retryable failure off to nextAttempt, leaving the row
// pending so the next due scan re-claims it.
func (s *Store) RescheduleAlert(ctx context.Context, id int64, nextAttempt time.Time) error {
	if err := s.q.RescheduleAlert(ctx, db.RescheduleAlertParams{
		ID:            id,
		NextAttemptAt: pgtype.Timestamptz{Time: nextAttempt, Valid: true},
	}); err != nil {
		return fmt.Errorf("store: reschedule alert: %w", err)
	}
	return nil
}

// FailAlert terminally fails an alert (retries exhausted or a permanent error)
// and, in the same transaction, records an "alert_failed" event so the failure
// is visible on the monitor's timeline. monitorID is the alert's monitor;
// reason is a short, safe description for the event message (never a secret).
func (s *Store) FailAlert(ctx context.Context, id int64, monitorID, reason string) error {
	mID, err := pgUUID(monitorID)
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(q *db.Queries) error {
		if err := q.MarkAlertFailed(ctx, id); err != nil {
			return fmt.Errorf("store: mark alert failed: %w", err)
		}
		msg := "Alert delivery failed"
		if reason != "" {
			msg = "Alert delivery failed: " + reason
		}
		if _, err := recordEvent(ctx, q, mID, "alert_failed", msg); err != nil {
			return err
		}
		return nil
	})
}

// ResolveDowntime returns how long the monitor was down before the recovery at
// recoveredAt, by finding the most recent "down" event at or before it. A zero
// duration is returned (no error) when no prior down event is found — the
// recovery email then simply omits a precise duration rather than failing.
func (s *Store) ResolveDowntime(ctx context.Context, monitorID string, recoveredAt time.Time) (time.Duration, error) {
	mID, err := pgUUID(monitorID)
	if err != nil {
		return 0, err
	}
	ev, err := s.q.LatestDownEventBefore(ctx, db.LatestDownEventBeforeParams{
		MonitorID: mID,
		Before:    pgtype.Timestamptz{Time: recoveredAt, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("store: latest down event: %w", err)
	}
	return max(recoveredAt.Sub(ev.CreatedAt.Time), 0), nil
}

// EnqueueDownReminders inserts a "still down" reminder alert for every monitor
// that is down, unmuted, has an enabled cadence, and whose most recent alert is
// older than that cadence. It returns the number of reminders queued. Muted or
// reminders-disabled monitors are skipped by the query, satisfying the "reminder
// suppressed while muted" requirement. now is injected for test determinism.
func (s *Store) EnqueueDownReminders(ctx context.Context, now time.Time) (int, error) {
	var count int
	err := s.withTx(ctx, func(q *db.Queries) error {
		rows, err := q.EnqueueDownReminders(ctx, pgtype.Timestamptz{Time: now, Valid: true})
		if err != nil {
			return fmt.Errorf("store: enqueue down reminders: %w", err)
		}
		count = len(rows)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// decodeTLSExpiresAt extracts tls_expires_at from a tls_expiry event's meta
// JSON (store/prober.go's tlsExpiryMeta). Any other event's meta shape
// (e.g. config_change's field list) simply fails to unmarshal into
// tlsExpiryMeta and returns nil — this is only ever called for a job the
// caller already knows is a tls_expiry alert in practice, but decoding
// unconditionally keeps ClaimDueAlerts a single pass over the rows.
func decodeTLSExpiresAt(meta []byte) *time.Time {
	if len(meta) == 0 {
		return nil
	}
	var m tlsExpiryMeta
	if err := json.Unmarshal(meta, &m); err != nil || m.TLSExpiresAt.IsZero() {
		return nil
	}
	return &m.TLSExpiresAt
}
