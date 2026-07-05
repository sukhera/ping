-- Dashboard uptime bar (PING-013): last-N-days rollups for a batch of
-- monitors in one query (avoids N+1 across a list page). Table is written by
-- the nightly rollup job (PING-020).
-- name: ListDailyStatsByMonitorIDs :many
SELECT * FROM daily_stats
WHERE monitor_id = ANY(sqlc.arg(monitor_ids)::uuid[])
  AND day >= sqlc.arg(since)::date
ORDER BY monitor_id, day ASC;

-- RollupCheckinCounts (PING-020): per-monitor checkin/failure counts for one
-- UTC day, from raw heartbeat checkins. Only monitors with at least one
-- checkin that day appear — the rollup job upserts these and leaves monitors
-- with zero heartbeat activity untouched (an HTTP monitor never has rows
-- here; a heartbeat monitor with no pings that day gets no row either, same
-- as today's "absent means no data" convention).
-- name: RollupCheckinCounts :many
SELECT
    monitor_id,
    COUNT(*)::int AS checkins,
    COUNT(*) FILTER (WHERE kind = 'fail')::int AS failures
FROM checkins
WHERE created_at >= sqlc.arg(day_start)::timestamptz
  AND created_at <  sqlc.arg(day_end)::timestamptz
GROUP BY monitor_id;

-- RollupProbeStats (PING-020): per-monitor probe counts + latency percentiles
-- for one UTC day, from raw HTTP probe results. "Failures" counts probes with
-- ok = false; latency percentiles are computed over successful probes only
-- (a failed probe has no meaningful latency signal), matching
-- LatencySeriesByMonitor's convention. A monitor with probes but zero
-- successes yields NULL percentiles (the LEFT JOIN's right side has no row),
-- which the rollup job stores as NULL — percentiles are computed in the
-- "successes" CTE via a WHERE filter, not a FILTER clause, because sqlc
-- cannot infer PERCENTILE_CONT ... FILTER(...) as nullable and would
-- generate a non-nullable int32 that panics on Scan the first time a
-- monitor's day has zero successful probes.
-- name: RollupProbeStats :many
WITH counts AS (
    SELECT
        monitor_id,
        COUNT(*)::int AS checkins,
        COUNT(*) FILTER (WHERE ok = false)::int AS failures
    FROM probe_results
    WHERE created_at >= sqlc.arg(day_start)::timestamptz
      AND created_at <  sqlc.arg(day_end)::timestamptz
    GROUP BY monitor_id
), successes AS (
    SELECT
        monitor_id,
        PERCENTILE_CONT(0.5)  WITHIN GROUP (ORDER BY latency_ms)::int AS latency_p50,
        PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY latency_ms)::int AS latency_p95
    FROM probe_results
    WHERE created_at >= sqlc.arg(day_start)::timestamptz
      AND created_at <  sqlc.arg(day_end)::timestamptz
      AND ok = true
    GROUP BY monitor_id
)
SELECT
    counts.monitor_id,
    counts.checkins,
    counts.failures,
    successes.latency_p50,
    successes.latency_p95
FROM counts
LEFT JOIN successes ON successes.monitor_id = counts.monitor_id;

-- RollupDowntimeSeconds (PING-020): per-monitor downtime seconds overlapping
-- one UTC day, computed from down/up transition events rather than raw
-- checkins/probes — downtime is a state duration, not something a row count
-- can capture. For every 'down' event, LEAD() finds the next event of either
-- kind on that monitor; if it's an 'up', that closes the outage, otherwise
-- (no later event yet — still down, or the next event is unrelated) the
-- outage is treated as open through day_end, matching "still down" reminders
-- counting as ongoing downtime for the day's rollup. Each outage's
-- [down_at, resolved_at) is clamped to [day_start, day_end) via
-- LEAST/GREATEST before summing, which is what makes downtime split
-- correctly across a day boundary (AC-2): an outage spanning midnight
-- contributes its pre-midnight seconds to day N and its post-midnight
-- seconds to day N+1 when this query is run once per day.
-- name: RollupDowntimeSeconds :many
WITH transitions AS (
    SELECT
        monitor_id,
        type,
        created_at,
        LEAD(created_at) OVER (PARTITION BY monitor_id ORDER BY created_at) AS next_at,
        LEAD(type) OVER (PARTITION BY monitor_id ORDER BY created_at) AS next_type
    FROM events
    WHERE type IN ('down', 'up')
      -- One extra day of lookback lets an outage that started before
      -- day_start still be found and clamped correctly; events far enough in
      -- the past to never overlap [day_start, day_end) are excluded by the
      -- final WHERE below instead of here, so LEAD() still sees the true
      -- next transition even if it's outside this window.
      AND created_at < sqlc.arg(day_end)::timestamptz
), outages AS (
    SELECT
        monitor_id,
        created_at AS down_at,
        COALESCE(
            CASE WHEN next_type = 'up' THEN next_at END,
            sqlc.arg(day_end)::timestamptz
        ) AS resolved_at
    FROM transitions
    WHERE type = 'down'
)
SELECT
    monitor_id,
    SUM(
        GREATEST(0, EXTRACT(EPOCH FROM (
            LEAST(resolved_at, sqlc.arg(day_end)::timestamptz) -
            GREATEST(down_at, sqlc.arg(day_start)::timestamptz)
        )))
    )::int AS downtime_s
FROM outages
WHERE resolved_at > sqlc.arg(day_start)::timestamptz
  AND down_at < sqlc.arg(day_end)::timestamptz
GROUP BY monitor_id;

-- UpsertDailyStat (PING-020): the rollup job's write path. ON CONFLICT DO
-- UPDATE makes a re-run for the same (monitor_id, day) replace rather than
-- double-count, satisfying the "rollup is idempotent" AC. latency_p50/p95 are
-- nullable (an all-heartbeat day, or an HTTP day with zero successful
-- probes, has no latency signal).
-- name: UpsertDailyStat :exec
INSERT INTO daily_stats (monitor_id, day, checkins, failures, downtime_s, latency_p50, latency_p95)
VALUES (
    sqlc.arg(monitor_id), sqlc.arg(day), sqlc.arg(checkins), sqlc.arg(failures),
    sqlc.arg(downtime_s), sqlc.narg(latency_p50), sqlc.narg(latency_p95)
)
ON CONFLICT (monitor_id, day) DO UPDATE SET
    checkins    = EXCLUDED.checkins,
    failures    = EXCLUDED.failures,
    downtime_s  = EXCLUDED.downtime_s,
    latency_p50 = EXCLUDED.latency_p50,
    latency_p95 = EXCLUDED.latency_p95;
