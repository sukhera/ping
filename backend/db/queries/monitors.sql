-- name: CreateMonitor :one
INSERT INTO monitors (
    user_id, kind, slug, name,
    schedule_kind, period_s, cron_expr, tz, grace_s,
    url, method, interval_s, timeout_s, fail_threshold, http_config,
    auto_resume
) VALUES (
    sqlc.arg(user_id), sqlc.arg(kind), sqlc.arg(slug), sqlc.arg(name),
    sqlc.narg(schedule_kind), sqlc.narg(period_s), sqlc.narg(cron_expr),
    COALESCE(NULLIF(sqlc.arg(tz)::text, ''), 'UTC'), sqlc.narg(grace_s),
    sqlc.narg(url), sqlc.narg(method), sqlc.narg(interval_s), sqlc.narg(timeout_s), sqlc.narg(fail_threshold),
    COALESCE(sqlc.narg(http_config)::jsonb, '{}'::jsonb),
    COALESCE(sqlc.narg(auto_resume)::boolean, true)
)
RETURNING *;

-- name: GetMonitorByID :one
SELECT * FROM monitors
WHERE id = $1;

-- name: GetMonitorBySlug :one
SELECT * FROM monitors
WHERE slug = $1;

-- Ingest fast path (PING-008): fetch the monitor and lock its row for the rest
-- of the transaction. Plain FOR UPDATE (block, don't skip) — every ping must be
-- processed, and the lock serializes concurrent pings on the same slug so the
-- read-state / transition / write is atomic (no duplicate recovery/down rows).
-- name: GetMonitorBySlugForUpdate :one
SELECT * FROM monitors
WHERE slug = $1
FOR UPDATE;

-- name: ListMonitorsByUser :many
SELECT * FROM monitors
WHERE user_id = $1
ORDER BY created_at DESC;

-- Cursor pagination on (created_at, id) rather than OFFSET: created_at alone
-- isn't unique, so the composite key keeps page boundaries stable under
-- concurrent inserts. sqlc.narg(cursor_created_at)/sqlc.narg(cursor_id) are
-- both NULL on the first page (no WHERE filter applied).
-- name: ListMonitorsByUserPage :many
SELECT * FROM monitors
WHERE user_id = sqlc.arg(user_id)
  AND (
    sqlc.narg(cursor_created_at)::timestamptz IS NULL
    OR (created_at, id) < (sqlc.narg(cursor_created_at)::timestamptz, sqlc.narg(cursor_id)::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: UpdateMonitor :one
-- Partial update: sqlc.narg fields left NULL keep their current value via
-- COALESCE. Ownership-enforced by the id + user_id WHERE, matching
-- Pause/Resume/Delete — a foreign monitor_id updates zero rows rather than
-- erroring, so the store layer must check RowsAffected/pgx.ErrNoRows.
UPDATE monitors
SET
    name           = COALESCE(sqlc.narg(name), name),
    schedule_kind  = COALESCE(sqlc.narg(schedule_kind), schedule_kind),
    period_s       = COALESCE(sqlc.narg(period_s), period_s),
    cron_expr      = COALESCE(sqlc.narg(cron_expr), cron_expr),
    tz             = COALESCE(sqlc.narg(tz), tz),
    grace_s        = COALESCE(sqlc.narg(grace_s), grace_s),
    url            = COALESCE(sqlc.narg(url), url),
    method         = COALESCE(sqlc.narg(method), method),
    interval_s     = COALESCE(sqlc.narg(interval_s), interval_s),
    timeout_s      = COALESCE(sqlc.narg(timeout_s), timeout_s),
    fail_threshold = COALESCE(sqlc.narg(fail_threshold), fail_threshold),
    http_config    = COALESCE(sqlc.narg(http_config)::jsonb, http_config),
    auto_resume    = COALESCE(sqlc.narg(auto_resume)::boolean, auto_resume),
    updated_at     = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
RETURNING *;

-- Ingest fast path (PING-008): apply a check-in's effect to the monitor row we
-- already hold locked via GetMonitorBySlugForUpdate. next_deadline is NULL on a
-- fail; paused_at is always cleared (any check-in auto-resumes the monitor).
-- name: UpdateMonitorOnCheckin :exec
-- paused_at is cleared (auto-resume) only when the monitor's auto_resume flag is
-- set (PING-010). With auto_resume=false a check-in still records state and
-- re-arms next_deadline, but leaves paused_at set — the scheduler keeps skipping
-- it (its claims filter paused_at IS NULL), so the future deadline is inert
-- until the monitor is explicitly resumed.
UPDATE monitors
SET state           = sqlc.arg(state),
    last_checkin_at = sqlc.arg(last_checkin_at),
    next_deadline   = sqlc.narg(next_deadline),
    fail_streak     = sqlc.arg(fail_streak),
    paused_at       = CASE WHEN auto_resume THEN NULL ELSE paused_at END,
    updated_at      = now()
WHERE id = sqlc.arg(id);

-- Scheduler up->late (PING-009): re-arm next_deadline to the DOWN threshold
-- (occurrence + grace, computed by the caller) and flip state. paused_at is
-- deliberately untouched — the scheduler must never resume a paused monitor
-- (unlike UpdateMonitorOnCheckin, which auto-resumes on a real check-in).
-- name: MarkMonitorLate :exec
UPDATE monitors
SET state         = 'late',
    next_deadline = sqlc.arg(next_deadline),
    updated_at    = now()
WHERE id = sqlc.arg(id);

-- Scheduler late->down (PING-009): clear the deadline and bump the fail streak.
-- paused_at untouched (see MarkMonitorLate).
-- name: MarkMonitorDown :exec
UPDATE monitors
SET state         = 'down',
    next_deadline = NULL,
    fail_streak   = fail_streak + 1,
    updated_at    = now()
WHERE id = $1;

-- Pause (PING-010): set the flag only — state is deliberately untouched (paused
-- is a flag, not a state, per §2.3). RETURNING so the handler can echo the
-- updated monitor. Zero rows affected (foreign/missing id) => ErrNotFound.
-- name: PauseMonitor :one
UPDATE monitors
SET paused_at = now(), updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
RETURNING *;

-- Resume (PING-010): a clean restart — clear the flag, set state='up', and
-- re-arm next_deadline from the resume moment (computed by the caller via
-- nextDeadlineFor; NULL for non-heartbeat / unscheduled). Re-arming from now is
-- what stops a monitor paused past its deadline from tripping late/down the
-- instant it resumes (AC-2).
-- name: ResumeMonitor :one
UPDATE monitors
SET paused_at     = NULL,
    state         = 'up',
    next_deadline = sqlc.narg(next_deadline),
    updated_at    = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
RETURNING *;

-- name: MuteMonitor :one
UPDATE monitors
SET alerts_muted = true, updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
RETURNING *;

-- name: UnmuteMonitor :one
UPDATE monitors
SET alerts_muted = false, updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
RETURNING *;

-- :execrows (not :exec) so the store layer can tell "deleted" apart from
-- "no matching row" (foreign or already-deleted id) via RowsAffected.
-- name: DeleteMonitor :execrows
DELETE FROM monitors
WHERE id = $1 AND user_id = $2;

-- Worker scan #1: scheduler claims heartbeat monitors past their deadline.
-- Uses idx_monitors_due (next_deadline, partial WHERE state IN ('up','late') AND paused_at IS NULL).
-- The evaluation clock is passed in (sqlc.arg(now)) rather than SQL now() so the
-- claim and the in-Go transition math share one consistent instant, and tests
-- can drive it deterministically. Production passes time.Now().
-- name: ClaimDueMonitors :many
SELECT * FROM monitors
WHERE next_deadline < sqlc.arg(now)
  AND state IN ('up', 'late')
  AND paused_at IS NULL
ORDER BY next_deadline
LIMIT sqlc.arg(page_limit)
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
