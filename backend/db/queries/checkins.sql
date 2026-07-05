-- Ingest fast path (PING-008): every ping records exactly one checkin row.
-- source_ip (INET) / user_agent / body are nullable; the store passes NULL
-- when absent. body is already truncated to 10 KB by the server handler.
-- name: InsertCheckin :one
INSERT INTO checkins (monitor_id, kind, source_ip, user_agent, body)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- Check-in log (PING-014): one monitor's check-ins, newest first. Ownership
-- is checked in the handler, so this filters by monitor_id only (plus an
-- optional cursor). Uses idx_checkins_monitor (monitor_id, created_at DESC).
-- Cursor is the BIGSERIAL id (monotonic), so id < cursor paginates strictly,
-- matching ListEventsByMonitorPage's convention.
-- name: ListCheckinsByMonitorPage :many
SELECT id, monitor_id, kind, source_ip, user_agent, body, created_at
FROM checkins
WHERE monitor_id = sqlc.arg(monitor_id)
  AND (sqlc.narg(cursor_id)::bigint IS NULL OR id < sqlc.narg(cursor_id)::bigint)
ORDER BY id DESC
LIMIT sqlc.arg(page_limit);

-- DeleteOldCheckinsBatch (PING-020 retention): deletes up to batch_limit rows
-- older than the cutoff, chosen via a subquery LIMIT rather than a bare
-- DELETE ... WHERE created_at < cutoff so one call never locks more than
-- batch_limit rows — the caller (store) loops this until it deletes fewer
-- than batch_limit rows, keeping each individual statement's lock short-lived
-- on a table that ingest is concurrently writing to. daily_stats has already
-- captured this data by the time it's pruned, so no aggregate is lost.
-- name: DeleteOldCheckinsBatch :execrows
DELETE FROM checkins
WHERE id IN (
    SELECT id FROM checkins
    WHERE created_at < sqlc.arg(cutoff)::timestamptz
    LIMIT sqlc.arg(batch_limit)
);
