-- name: CreateMonitor :one
INSERT INTO monitors (
    user_id, kind, slug, name,
    schedule_kind, period_s, cron_expr, tz, grace_s,
    url, method, interval_s, timeout_s, fail_threshold, http_config
) VALUES (
    sqlc.arg(user_id), sqlc.arg(kind), sqlc.arg(slug), sqlc.arg(name),
    sqlc.narg(schedule_kind), sqlc.narg(period_s), sqlc.narg(cron_expr),
    COALESCE(NULLIF(sqlc.arg(tz)::text, ''), 'UTC'), sqlc.narg(grace_s),
    sqlc.narg(url), sqlc.narg(method), sqlc.narg(interval_s), sqlc.narg(timeout_s), sqlc.narg(fail_threshold),
    COALESCE(sqlc.narg(http_config)::jsonb, '{}'::jsonb)
)
RETURNING *;

-- name: GetMonitorByID :one
SELECT * FROM monitors
WHERE id = $1;

-- name: GetMonitorBySlug :one
SELECT * FROM monitors
WHERE slug = $1;

-- name: ListMonitorsByUser :many
SELECT * FROM monitors
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: PauseMonitor :exec
UPDATE monitors
SET paused_at = now()
WHERE id = $1 AND user_id = $2;

-- name: ResumeMonitor :exec
UPDATE monitors
SET paused_at = NULL
WHERE id = $1 AND user_id = $2;

-- name: DeleteMonitor :exec
DELETE FROM monitors
WHERE id = $1 AND user_id = $2;

-- Worker scan #1: scheduler claims heartbeat monitors past their deadline.
-- Uses idx_monitors_due (next_deadline, partial WHERE state IN ('up','late') AND paused_at IS NULL).
-- name: ClaimDueMonitors :many
SELECT * FROM monitors
WHERE next_deadline < now()
  AND state IN ('up', 'late')
  AND paused_at IS NULL
ORDER BY next_deadline
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- Worker scan #2: prober claims http monitors due for their next probe.
-- Uses idx_monitors_probe_due (next_probe_at, partial WHERE kind = 'http' AND paused_at IS NULL).
-- name: ClaimDueProbes :many
SELECT * FROM monitors
WHERE next_probe_at < now()
  AND kind = 'http'
  AND paused_at IS NULL
ORDER BY next_probe_at
LIMIT $1
FOR UPDATE SKIP LOCKED;
