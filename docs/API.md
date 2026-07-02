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
