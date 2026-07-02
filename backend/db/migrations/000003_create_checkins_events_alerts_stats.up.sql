CREATE TABLE checkins (
    id         BIGSERIAL PRIMARY KEY,
    monitor_id UUID NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    source_ip  INET,
    user_agent TEXT,
    body       TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT checkins_kind_check CHECK (kind IN ('success', 'start', 'fail'))
);

CREATE INDEX idx_checkins_monitor ON checkins (monitor_id, created_at DESC);

CREATE TABLE probe_results (
    id             BIGSERIAL PRIMARY KEY,
    monitor_id     UUID NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    ok             BOOLEAN NOT NULL,
    http_status    INTEGER,
    latency_ms     INTEGER,
    error          TEXT,
    tls_expires_at TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_probe_results_mon ON probe_results (monitor_id, created_at DESC);

CREATE TABLE events (
    id         BIGSERIAL PRIMARY KEY,
    monitor_id UUID NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    type       TEXT NOT NULL,
    message    TEXT NOT NULL,
    meta       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_events_monitor ON events (monitor_id, created_at DESC);

CREATE TABLE alerts (
    id              BIGSERIAL PRIMARY KEY,
    monitor_id      UUID NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    event_id        BIGINT NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    channel         TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT alerts_status_check CHECK (status IN ('pending', 'sent', 'failed'))
);

CREATE INDEX idx_alerts_pending ON alerts (next_attempt_at) WHERE status = 'pending';
CREATE INDEX idx_alerts_monitor ON alerts (monitor_id);
CREATE INDEX idx_alerts_event   ON alerts (event_id);

CREATE TABLE daily_stats (
    monitor_id   UUID NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    day          DATE NOT NULL,
    checkins     INTEGER NOT NULL DEFAULT 0,
    failures     INTEGER NOT NULL DEFAULT 0,
    downtime_s   INTEGER NOT NULL DEFAULT 0,
    latency_p50  INTEGER,
    latency_p95  INTEGER,

    PRIMARY KEY (monitor_id, day)
);
