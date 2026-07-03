-- Outbox row for an alerting transition (PING-009/011). status defaults
-- 'pending' and next_attempt_at defaults now(): the ingest/scheduler fast paths
-- only enqueue, they never dispatch. channel is the sentinel 'default' until a
-- future ticket introduces real notification channels and fans a transition out
-- across them.
-- name: InsertAlert :one
INSERT INTO alerts (monitor_id, event_id, channel)
VALUES ($1, $2, $3)
RETURNING *;

-- ClaimDueAlerts is the alerter worker's scan (PING-012): it claims pending
-- outbox rows that are due, locking each so a second replica skips it, and joins
-- everything one send needs — recipient, monitor identity/state/mute flag, and
-- the triggering event — in a single round-trip. FOR UPDATE OF a locks only the
-- alerts row (not the joined monitors/users/events), and SKIP LOCKED lets other
-- workers claim different rows concurrently. Uses idx_alerts_pending
-- (next_attempt_at) WHERE status = 'pending'.
-- name: ClaimDueAlerts :many
SELECT a.id, a.monitor_id, a.event_id, a.channel, a.status, a.attempts,
       a.next_attempt_at, a.sent_at, a.created_at, a.is_reminder,
       u.email            AS recipient,
       m.name             AS monitor_name,
       m.slug             AS monitor_slug,
       m.state            AS monitor_state,
       m.alerts_muted     AS alerts_muted,
       e.type             AS event_type,
       e.message          AS event_message,
       e.created_at       AS event_created_at
FROM alerts a
JOIN monitors m ON m.id = a.monitor_id
JOIN users u    ON u.id = m.user_id
JOIN events e   ON e.id = a.event_id
WHERE a.status = 'pending'
  AND a.next_attempt_at < sqlc.arg(now)::timestamptz
ORDER BY a.next_attempt_at
LIMIT sqlc.arg(page_limit)
FOR UPDATE OF a SKIP LOCKED;

-- MarkAlertSent finalizes a delivered alert. attempts is bumped so it reflects
-- the total number of send tries including the successful one.
-- name: MarkAlertSent :exec
UPDATE alerts
SET status = 'sent', sent_at = now(), attempts = attempts + 1
WHERE id = $1;

-- SuppressAlert resolves an alert that must not be delivered (monitor muted)
-- without sending it. It reuses the terminal 'sent' status so the row leaves the
-- pending outbox and is never retried; sent_at stays NULL to distinguish a
-- suppressed alert from a delivered one. The transition/event was already
-- recorded upstream, so no notification is owed.
-- name: SuppressAlert :exec
UPDATE alerts
SET status = 'sent'
WHERE id = $1;

-- RescheduleAlert backs a failed-but-retryable alert off to a later attempt.
-- attempts is bumped and next_attempt_at moved forward; the row stays 'pending'
-- so the next due scan re-claims it.
-- name: RescheduleAlert :exec
UPDATE alerts
SET attempts = attempts + 1, next_attempt_at = sqlc.arg(next_attempt_at)::timestamptz
WHERE id = $1;

-- MarkAlertFailed terminally fails an alert (retries exhausted or a permanent
-- delivery error). The alerter also writes an 'alert_failed' event in the same
-- transaction so the failure is visible on the monitor's timeline.
-- name: MarkAlertFailed :exec
UPDATE alerts
SET status = 'failed', attempts = attempts + 1
WHERE id = $1;

-- LatestDownEventBefore finds the most recent 'down' event for a monitor at or
-- before a given time — the start of the current outage. The alerter uses it to
-- compute recovery downtime ("recovered after 42m") for an 'up' alert.
-- name: LatestDownEventBefore :one
SELECT id, monitor_id, type, message, meta, created_at
FROM events
WHERE monitor_id = $1
  AND type = 'down'
  AND created_at <= sqlc.arg(before)::timestamptz
ORDER BY created_at DESC
LIMIT 1;

-- EnqueueDownReminders inserts a fresh pending reminder alert for every monitor
-- that is still down, not muted, and has an enabled reminder cadence whose most
-- recent alert (down or prior reminder) is older than that cadence. It reuses
-- the monitor's latest 'down' event as the alert's event_id so the reminder ties
-- back to the outage. The NOT EXISTS guard prevents piling up duplicate pending
-- reminders when the alerter is behind. Returns the inserted rows so the worker
-- can log how many reminders it queued.
-- name: EnqueueDownReminders :many
INSERT INTO alerts (monitor_id, event_id, channel, is_reminder)
SELECT m.id, de.id, 'default', true
FROM monitors m
JOIN LATERAL (
    SELECT e.id, e.created_at
    FROM events e
    WHERE e.monitor_id = m.id AND e.type = 'down'
    ORDER BY e.created_at DESC
    LIMIT 1
) de ON true
WHERE m.state = 'down'
  AND m.alerts_muted = false
  AND m.reminder_every_s > 0
  -- No alert enqueued for this monitor within the cadence window. This one
  -- predicate both enforces the cadence AND prevents stacking reminders: a
  -- reminder just inserted has created_at ~= now, so it falls inside the window
  -- and blocks the next enqueue until a full cadence has elapsed. It also means
  -- the fresh down alert (created at the transition) suppresses reminders until
  -- one cadence after the outage began.
  AND NOT EXISTS (
      SELECT 1 FROM alerts a
      WHERE a.monitor_id = m.id
        AND a.created_at > sqlc.arg(now)::timestamptz - make_interval(secs => m.reminder_every_s)
  )
RETURNING *;
