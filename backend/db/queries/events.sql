-- Timeline events for a monitor (state transitions, config changes, etc.).
-- meta defaults to an empty object when the caller passes NULL.
-- name: InsertEvent :one
INSERT INTO events (monitor_id, type, message, meta)
VALUES ($1, $2, $3, COALESCE(sqlc.narg(meta)::jsonb, '{}'::jsonb))
RETURNING *;
