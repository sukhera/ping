# API

## Ping ingestion (PING-008)

The ingestion endpoints are the hot path: a cron job, script, or container pings
a monitor's URL to report that it ran. They are **public and unauthenticated** —
the secret is the 16-character random slug in the URL itself. Every well-formed
request gets a **tiny `200`**; the endpoints never reject a ping with a 4xx/5xx
that a client cron might retry-storm on.

Base URL of a monitor: `POST/GET/HEAD <PING_BASE_URL>/p/<slug>` (returned as
`ping_url` on monitor create/get).

| Path | Meaning | Effect on monitor |
| --- | --- | --- |
| `/p/<slug>` | success | `state → up`, `next_deadline` re-armed from now, `fail_streak → 0`, auto-resume |
| `/p/<slug>/start` | job started | check-in recorded only; **no** state / deadline / pause change |
| `/p/<slug>/fail` | explicit failure | `state → down` immediately, `fail_streak++`, deadline cleared, auto-resume |
| `/p/<slug>/<exit-code>` | numeric exit code | `0` → treated as success; any non-zero → treated as failure |

All four accept **GET, POST, and HEAD**. A non-numeric trailing segment that is
not `start`/`fail` falls through to the `<exit-code>` route and is treated as a
success (a ping is never rejected).

### Body capture

The request body is **captured up to 10 KB and truncated** past that — never
rejected for being too large. Bodies are stored verbatim as text (binary /
control characters are preserved and are the renderer's responsibility to
escape). `HEAD` requests carry no body and none is read. Source IP (from the
direct connection's remote address) and `User-Agent` are recorded alongside.

### Unknown slug → `200`, nothing recorded

A ping to a slug that does not exist returns `200` exactly like a real one, and
records **nothing** (no check-in, no monitor change, no event). This is
deliberate **anti-enumeration**, matching Healthchecks' behavior: a `404` on
unknown slugs would let an attacker probe which slugs are live. Because the slug
is the only credential, the endpoint must not leak slug existence through its
status code.

### No alert dispatch on the ingest path (fast path)

Ingestion does the minimum synchronous work: record the check-in, transition
state, recompute the deadline. On an actual `up ↔ down` transition it writes a
timeline **event** plus a **pending outbox `alerts` row** — but it never sends
anything. The alerter worker (PING-012) claims pending rows and dispatches them.
Duplicate transitions are deduped: a repeated `/fail` on an already-`down`
monitor still records the check-in and increments `fail_streak`, but writes no
second event/alert. Concurrent pings on the same slug are serialized by a
`SELECT … FOR UPDATE` on the monitor row, so a recovery/down event is emitted at
most once.

Until PING-012 introduces real notification channels, outbox rows use a sentinel
`channel = 'default'`.

### Rate limiting

Pings are rate-limited **per source IP** (generous: 120/minute), sharing the
Redis fixed-window limiter used by auth. It **fails open** — a Redis outage never
blocks legitimate check-ins. Over the limit returns `429` with `Retry-After`.

## API keys + management-API auth (PING-016)

Every `/api/v1/monitors/*`, `/api/v1/schedule/describe`, `/api/v1/alerting/test`,
and `/api/v1/events` endpoint accepts **either** credential:

- `Authorization: Bearer <JWT access token>` — the web app's session.
- `Authorization: Bearer pk_<64 hex chars>` — a long-lived API key, for
  scripts/CI. Full monitor CRUD works with just a key, no browser session
  needed.

API keys are **managed with a JWT session only** (`/api/v1/apikeys/*` below
rejects a `pk_...` bearer with `401`) — a leaked key can use the management
API but can never mint or revoke other keys for the account.

A key is shown **exactly once**, at creation — only its SHA-256 hash is ever
stored. There is no way to recover a lost key; revoke it and create a new one.
A revoked (or unknown) key is rejected on the very next request — there is no
cache window. Each key has its own rate limit (300 req/min), independent of
other keys on the same account, so one misbehaving script can't starve
another.

| Endpoint | Auth | Effect |
| --- | --- | --- |
| `POST /api/v1/apikeys` | JWT only | Body `{"label": "..."}`. Returns `201` with `{"id", "label", "key", "created_at"}` — `key` is the plaintext, never returned again. |
| `GET /api/v1/apikeys` | JWT only | Lists the caller's keys, newest first. Never includes the hash or plaintext. Revoked keys stay listed with `revoked_at` set (an audit trail, not silently removed). |
| `DELETE /api/v1/apikeys/{id}` | JWT only | Revokes a key owned by the caller. `204` on success, `404` for a foreign or already-revoked key. |

### curl examples

Mint a key (needs a JWT from `/api/v1/auth/login`):

```sh
curl -sX POST http://localhost:8080/api/v1/apikeys \
  -H "Authorization: Bearer $JWT" \
  -H "Content-Type: application/json" \
  -d '{"label":"ci runner"}'
# {"id":"...", "label":"ci runner", "key":"pk_...", "created_at":"..."}
```

Full monitor CRUD using only the key — no JWT involved:

```sh
export PK=pk_...   # from the response above

curl -sX POST http://localhost:8080/api/v1/monitors \
  -H "Authorization: Bearer $PK" -H "Content-Type: application/json" \
  -d '{"kind":"heartbeat","name":"nightly backup","schedule_kind":"period","period_s":300,"tz":"UTC","grace_s":60}'

curl -s http://localhost:8080/api/v1/monitors -H "Authorization: Bearer $PK"

curl -sX PATCH http://localhost:8080/api/v1/monitors/<id> \
  -H "Authorization: Bearer $PK" -H "Content-Type: application/json" \
  -d '{"name":"renamed"}'

curl -sX DELETE http://localhost:8080/api/v1/monitors/<id> -H "Authorization: Bearer $PK"
```

Revoke the key (JWT session required):

```sh
curl -sX DELETE http://localhost:8080/api/v1/apikeys/<id> -H "Authorization: Bearer $JWT"
```

## Pause / resume / mute (PING-010)

All authenticated (Bearer access token) and owner-scoped: acting on another
user's monitor returns `403`, unauthenticated returns `401`. Each returns `200`
with the updated monitor body and records a timeline event.

| Endpoint | Effect |
| --- | --- |
| `POST /api/v1/monitors/{id}/pause` | Sets the paused flag. **`state` is left untouched** — paused is a flag, not a state; `display_state` becomes `"paused"`. The scheduler stops evaluating it (no late/down while paused), but check-ins still record. |
| `POST /api/v1/monitors/{id}/resume` | A clean restart: clears the flag, sets `state` to `up`, and **re-arms `next_deadline` from now**, so a monitor paused past its old deadline does not trip late/down the instant it resumes. |
| `POST /api/v1/monitors/{id}/mute` / `.../unmute` | Toggles `alerts_muted`. Transitions are still recorded; alert dispatch (PING-012) will respect the flag. |

### Auto-resume on ping

Monitors have an `auto_resume` field (boolean, default `true`, settable on
create/update). When `true`, a successful check-in clears the paused flag
(auto-resume). When `false`, a check-in on a paused monitor still records and
re-arms the deadline, but the monitor **stays paused** until explicitly resumed.

## Event feed (PING-010)

Immutable timeline of everything that happened to a monitor: state transitions
(`up`, `late`, `down`), `pause`, `resume`, `mute`, `unmute`, and `config_change`
(with a `meta.fields` list of changed fields). Cursor-paginated by the opaque
`next_cursor` (newest first).

- `GET /api/v1/events` — global feed across all the caller's monitors. Filters:
  `?monitor=<id>`, `?type=<event-type>`. Pagination: `?cursor=`, `?limit=` (default 20, max 100).
- `GET /api/v1/monitors/{id}/events` — one monitor's feed (owner-scoped). Filter: `?type=`.

## Check-in log (PING-014)

- `GET /api/v1/monitors/{id}/checkins` — one monitor's raw check-ins
  (owner-scoped), newest first: `kind` (`success`/`start`/`fail`), `source_ip`,
  `user_agent`, `body` (truncated to 10 KB at ingest, passed through verbatim —
  the frontend renders it as escaped text, never `dangerouslySetInnerHTML`, so
  an HTML/script body is inert on screen). Cursor-paginated: `?cursor=`,
  `?limit=` (default 20, max 100).

## Probe log + latency series (PING-018)

For `kind: http` monitors, every probe attempt is recorded in `probe_results`
(status, latency, error, and TLS certificate expiry when the target is HTTPS).

- `GET /api/v1/monitors/{id}/probe-results` — one monitor's probe log
  (owner-scoped), newest first: `ok`, `http_status`, `latency_ms`, `error`,
  `tls_expires_at`. Filter: `?outcome=success` or `?outcome=fail` (omit for
  all). Cursor-paginated: `?cursor=`, `?limit=` (default 20, max 100).
- `GET /api/v1/monitors/{id}/latency` — pre-bucketed latency series for the
  detail-page chart. `?window=24h|7d|30d` (default `24h`); bucket width is
  chosen server-side per window (5m/1h/6h) so the point count stays
  chart-sized regardless of window length. Each point: `bucket_start`, `p50`,
  `p95`, `avg` (all milliseconds), `sample_count`. Only successful probes
  contribute — a failed probe has no meaningful latency to chart.

### TLS certificate expiry warnings

The prober records the leaf certificate's `NotAfter` on every successful
HTTPS probe. When a certificate is within 14 days of expiring, a `tls_expiry`
event + alert fires exactly once for that certificate — a monitor's
`tls_warned_expires_at` column tracks which expiry was last warned about, so
repeated probes against the same certificate don't re-alert every tick. When
the certificate is renewed (a later `NotAfter` on a later probe), the warning
automatically re-arms for the new expiry.

## Alerting (PING-011)

Alerts are delivered through the `alert.Channel` abstraction (`backend/alert`).
Email (SMTP) is the only implementation in v1; Slack/webhook channels slot in
later without schema changes (PRD F3.6). The package is pure delivery — it
renders templates and sends. Claiming outbox rows and scheduling retries is the
alerter worker's job (PING-012).

**Templates** (`alert.Render`), plain-text-first with a minimal DESIGN-tokened
HTML variant. Subjects follow PRD F3.2:

| Kind         | Subject example                                            |
| ------------ | ---------------------------------------------------------- |
| down         | `[DOWN] nightly-backup — missed check-in`                  |
| up           | `[UP] nightly-backup — recovered after 42m`                |
| tls_expiry   | `[TLS] api.example.com — certificate expires in 41 days`   |
| reminder     | `[DOWN] nightly-backup — still down after 1d 2h`           |
| test         | `[TEST] ping — SMTP delivery is working`                   |

All dynamic values are HTML-escaped in the HTML body. Subjects are RFC 2047
encoded (the em-dash is non-ASCII), so mail clients display them decoded.

**SMTP transport.** Chosen by port: `465` uses implicit TLS (SMTPS); any other
port (default `587`) uses STARTTLS when the server advertises it. Credentials
(PLAIN auth) are sent only over an encrypted connection and are never logged.
SMTP is optional — with `SMTP_HOST` unset the channel is disabled and the test
endpoint reports that clearly rather than failing opaquely.

**Retryable vs permanent errors** (`*alert.SendError`, `alert.IsRetryable`):
SMTP `4xx` replies and network/TLS failures are *retryable* (the mail server
may recover); `5xx` replies and any auth failure are *permanent* (the worker
fails them fast instead of burning retry attempts).

### `POST /api/v1/alerting/test`

Sends a verification email to the **authenticated caller's own account email**
(looked up server-side; no request body, so it can't be pointed at arbitrary
addresses). Requires auth.

- `200 {"delivered_to": "<caller email>"}` on success.
- `503` when SMTP is not configured, or when the mail server was temporarily
  unavailable (retryable).
- `502` when the mail server permanently rejected the message (e.g. bad
  credentials / relaying denied) — check SMTP settings.

The client-facing message is always safe: internal SMTP error text and
credentials never appear in the response or logs.

For local development, `docker compose up mailpit` provides an SMTP sink at
`localhost:1025` with a web UI at <http://localhost:8025>.
