-- Dashboard uptime bar (PING-013): last-N-days rollups for a batch of
-- monitors in one query (avoids N+1 across a list page). Table is written by
-- the nightly rollup job (PING-020, not yet built) — until then this always
-- returns zero rows, which callers must treat as "no data yet", not an error.
-- name: ListDailyStatsByMonitorIDs :many
SELECT * FROM daily_stats
WHERE monitor_id = ANY(sqlc.arg(monitor_ids)::uuid[])
  AND day >= sqlc.arg(since)::date
ORDER BY monitor_id, day ASC;
