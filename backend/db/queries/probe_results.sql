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

-- SetTLSWarnedExpiry records the tls_expires_at value a TLS-expiry warning was
-- just sent for (PING-018), so the next probe against the same certificate
-- (same tls_expires_at) does not re-warn. Passing a later tls_expires_at
-- (certificate renewed) naturally re-arms the warning on its own next expiry.
-- name: SetTLSWarnedExpiry :exec
UPDATE monitors
SET tls_warned_expires_at = sqlc.arg(tls_expires_at)::timestamptz
WHERE id = sqlc.arg(id);

-- ListProbeResultsByMonitor is the HTTP monitor probe log (PING-018):
-- cursor-paginated, newest first, optionally filtered to only failed
-- (?outcome=fail) or only successful (?outcome=success) probes. Uses
-- idx_probe_results_mon (monitor_id, created_at DESC); the id-based cursor
-- (WHERE id < cursor) keeps pagination stable under concurrent inserts, same
-- pattern as the events/checkins feeds.
-- name: ListProbeResultsByMonitor :many
SELECT * FROM probe_results
WHERE monitor_id = sqlc.arg(monitor_id)
  AND (sqlc.narg(ok)::boolean IS NULL OR ok = sqlc.narg(ok)::boolean)
  AND (sqlc.narg(cursor_id)::bigint IS NULL OR id < sqlc.narg(cursor_id)::bigint)
ORDER BY id DESC
LIMIT sqlc.arg(page_limit);

-- LatencySeriesByMonitor is the latency chart's backing query (PING-018):
-- buckets successful probes into fixed-width time buckets over [since, now)
-- and computes p50/p95/avg latency per bucket via PERCENTILE_CONT, so the
-- frontend gets pre-aggregated points instead of raw probe rows regardless of
-- the requested window (24h/7d/30d map to different bucket_seconds chosen by
-- the caller). Failed probes are excluded — a failure has no meaningful
-- latency signal and would skew the percentiles.
-- name: LatencySeriesByMonitor :many
SELECT
    to_timestamp(floor(extract(epoch FROM created_at) / sqlc.arg(bucket_seconds)::bigint) * sqlc.arg(bucket_seconds)::bigint) AS bucket_start,
    PERCENTILE_CONT(0.5)  WITHIN GROUP (ORDER BY latency_ms) AS p50,
    PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY latency_ms) AS p95,
    AVG(latency_ms)::float8 AS avg,
    COUNT(*) AS sample_count
FROM probe_results
WHERE monitor_id = sqlc.arg(monitor_id)
  AND ok = true
  AND created_at >= sqlc.arg(since)::timestamptz
GROUP BY bucket_start
ORDER BY bucket_start ASC;

-- DeleteOldProbeResultsBatch (PING-020 retention): same batched-delete shape
-- as DeleteOldCheckinsBatch — see that query's comment for why the subquery
-- LIMIT matters.
-- name: DeleteOldProbeResultsBatch :execrows
DELETE FROM probe_results
WHERE id IN (
    SELECT id FROM probe_results
    WHERE created_at < sqlc.arg(cutoff)::timestamptz
    LIMIT sqlc.arg(batch_limit)
);
