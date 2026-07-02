-- Worker scan #3: alerter claims pending outbox rows ready for (re)send.
-- Uses idx_alerts_pending (next_attempt_at, partial WHERE status = 'pending').
-- name: ClaimPendingAlerts :many
SELECT * FROM alerts
WHERE next_attempt_at < now()
  AND status = 'pending'
ORDER BY next_attempt_at
LIMIT $1
FOR UPDATE SKIP LOCKED;
