-- Worker scan #3: alerter claims pending outbox rows ready for (re)send.
-- Uses idx_alerts_pending (next_attempt_at, partial WHERE status = 'pending').
-- name: ClaimPendingAlerts :many
SELECT * FROM alerts
WHERE next_attempt_at < now()
  AND status = 'pending'
ORDER BY next_attempt_at
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- Outbox row for the alerter worker (PING-012). status defaults 'pending' and
-- next_attempt_at defaults now(): the ingest fast path only enqueues, it never
-- dispatches. channel is the sentinel 'default' until PING-012 introduces real
-- notification channels and fans a transition out across them.
-- name: InsertAlert :one
INSERT INTO alerts (monitor_id, event_id, channel)
VALUES ($1, $2, $3)
RETURNING *;
