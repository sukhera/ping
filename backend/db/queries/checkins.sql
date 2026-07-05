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
