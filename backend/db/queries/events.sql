-- Timeline events for a monitor (state transitions, config changes, etc.).
-- meta defaults to an empty object when the caller passes NULL.
-- name: InsertEvent :one
INSERT INTO events (monitor_id, type, message, meta)
VALUES ($1, $2, $3, COALESCE(sqlc.narg(meta)::jsonb, '{}'::jsonb))
RETURNING *;

-- Global event feed (PING-010): the caller's events across all their monitors,
-- newest first, with optional monitor and type filters. Cursor is the BIGSERIAL
-- id (monotonic), so id < cursor paginates strictly. Ordering by id DESC uses
-- idx_events_id.
-- name: ListEventsByUserPage :many
SELECT e.id, e.monitor_id, e.type, e.message, e.meta, e.created_at
FROM events e
JOIN monitors m ON m.id = e.monitor_id
WHERE m.user_id = sqlc.arg(user_id)
  AND (sqlc.narg(monitor_id)::uuid IS NULL OR e.monitor_id = sqlc.narg(monitor_id)::uuid)
  AND (sqlc.narg(event_type)::text IS NULL OR e.type = sqlc.narg(event_type))
  AND (sqlc.narg(cursor_id)::bigint IS NULL OR e.id < sqlc.narg(cursor_id)::bigint)
ORDER BY e.id DESC
LIMIT sqlc.arg(page_limit);

-- Per-monitor event feed (PING-010): ownership is checked in the handler, so
-- this filters by monitor_id only (plus optional type + cursor). Uses
-- idx_events_monitor (monitor_id, created_at DESC).
-- name: ListEventsByMonitorPage :many
SELECT id, monitor_id, type, message, meta, created_at
FROM events
WHERE monitor_id = sqlc.arg(monitor_id)
  AND (sqlc.narg(event_type)::text IS NULL OR type = sqlc.narg(event_type))
  AND (sqlc.narg(cursor_id)::bigint IS NULL OR id < sqlc.narg(cursor_id)::bigint)
ORDER BY id DESC
LIMIT sqlc.arg(page_limit);
