-- Prober (PING-017): one row per HTTP probe attempt. tls_expires_at is
-- populated for https targets so PING-018 can add expiry-warning logic on
-- top without another migration; it is NULL for http or on a failed probe.
-- name: InsertProbeResult :one
INSERT INTO probe_results (monitor_id, ok, http_status, latency_ms, error, tls_expires_at)
VALUES (sqlc.arg(monitor_id), sqlc.arg(ok), sqlc.narg(http_status), sqlc.narg(latency_ms), sqlc.narg(error), sqlc.narg(tls_expires_at))
RETURNING *;

-- Prober (PING-017): apply one probe's outcome to its monitor. fail_streak
-- and state are computed by the caller (store/prober.go) per the
-- confirmation-threshold logic (PRD F2.2): a single failure only increments
-- the streak, state flips to 'down' once the streak reaches fail_threshold; a
-- success always resets the streak to 0 and flips to 'up'. next_probe_at is
-- always re-armed to now + interval_s (computed by the caller) regardless of
-- outcome, so a failing target keeps being probed on its normal cadence.
-- name: UpdateMonitorOnProbe :exec
UPDATE monitors
SET state         = sqlc.arg(state),
    fail_streak   = sqlc.arg(fail_streak),
    next_probe_at = sqlc.arg(next_probe_at),
    updated_at    = now()
WHERE id = sqlc.arg(id);
