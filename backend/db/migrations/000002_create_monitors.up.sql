CREATE TABLE monitors (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind             TEXT NOT NULL,
    slug             TEXT NOT NULL,
    name             TEXT NOT NULL,

    -- schedule (heartbeat)
    schedule_kind    TEXT,
    period_s         INTEGER,
    cron_expr        TEXT,
    tz               TEXT NOT NULL DEFAULT 'UTC',
    grace_s          INTEGER,

    -- http monitor config
    url              TEXT,
    method           TEXT,
    interval_s       INTEGER,
    timeout_s        INTEGER,
    fail_threshold   INTEGER,
    http_config      JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- runtime state
    state            TEXT NOT NULL DEFAULT 'new',
    fail_streak      INTEGER NOT NULL DEFAULT 0,
    last_checkin_at  TIMESTAMPTZ,
    next_deadline    TIMESTAMPTZ,
    next_probe_at    TIMESTAMPTZ,
    alerts_muted     BOOLEAN NOT NULL DEFAULT false,
    paused_at        TIMESTAMPTZ,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT monitors_slug_unique UNIQUE (slug),
    CONSTRAINT monitors_kind_check CHECK (kind IN ('heartbeat', 'http')),
    CONSTRAINT monitors_state_check CHECK (state IN ('new', 'up', 'late', 'down')),
    CONSTRAINT monitors_schedule_kind_check CHECK (schedule_kind IS NULL OR schedule_kind IN ('period', 'cron')),
    CONSTRAINT monitors_method_check CHECK (method IS NULL OR method IN ('GET', 'POST', 'HEAD'))
);

CREATE INDEX idx_monitors_user       ON monitors (user_id);
CREATE INDEX idx_monitors_due        ON monitors (next_deadline) WHERE state IN ('up', 'late') AND paused_at IS NULL;
CREATE INDEX idx_monitors_probe_due  ON monitors (next_probe_at) WHERE kind = 'http' AND paused_at IS NULL;
