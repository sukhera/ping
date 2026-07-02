# ping — Product Requirements Document

**Version:** 1.0 · **Date:** 2026-07-01 · **Owner:** Ahmed Sukhera · **Status:** Draft for review

---

## 1. Overview

`ping` is a self-hostable cron-job and uptime monitor. It answers two questions every operator has:

1. **Did my scheduled job run?** Services hit a unique URL (`ping.yourdomain.com/<slug>`) on their schedule. If a check-in doesn't arrive in time, ping alerts you. (Dead-man's-switch / heartbeat monitoring — the Healthchecks.io model.)
2. **Is my site up?** ping actively probes external URLs on an interval and alerts on failures. (Active HTTP monitoring — the UptimeRobot model.)

Most tools do one or the other; ping does both in a single small, polished, self-hostable app. It is the third project in a portfolio (`shrt`, `scrt`) that demonstrates production-grade engineering on a deliberately compact scope: one Go binary, Postgres, Redis, and a Next.js frontend.

### Why it doesn't already exist (for us)

Healthchecks.io is excellent but is a large Django app; Uptime Kuma is Node/Vue and heartbeat monitoring is secondary. Neither matches this portfolio's stack or its bar for docs, tests, and design. ping is intentionally scoped to be understandable in one sitting yet complete enough to run in production.

## 2. Goals

- **G1 — Reliable dead-man's-switch monitoring.** A missed check-in is detected within 60 seconds of the grace deadline and produces exactly one "down" alert.
- **G2 — Trustworthy HTTP checks.** Configurable probes with sensible failure confirmation (no flapping alerts from a single blip).
- **G3 — 10-minute self-host.** `git clone` → `.env` → `make docker-up && make migrate-up && make dev` → first monitor created, matching the shrt quick-start experience.
- **G4 — Portfolio quality.** Same engineering bar as shrt: sqlc, migrations, OpenAPI spec, unit + E2E tests, architecture docs — plus a distinctly better product design (see `DESIGN.md`).
- **G5 — Calm product.** No noise: alert once on state change, remind sparingly, recover loudly.

## 3. Non-goals (v1)

- Multi-tenant SaaS features: teams, roles, billing, plan limits.
- Multi-region probing (self-hosted ping probes from wherever it runs).
- Status pages, Slack/webhook/SMS channels, on-call rotations (v2 candidates — the alert channel model must not preclude them).
- Mobile apps, browser extensions.
- Monitoring of non-HTTP protocols (TCP ports, ICMP, DNS) — v2 candidates.

## 4. Users

- **Primary — the self-hoster (that's us).** A developer running side projects: nightly backups, cert-renewal crons, a handful of public sites. Wants set-and-forget monitoring with zero SaaS bills, and email when something breaks.
- **Secondary — the OSS evaluator.** A developer or hiring manager skimming the repo. Judges the README, screenshots, code structure, and how fast they can run it locally.

## 5. Core concepts

**Monitor** — the central entity. One of two kinds:

- **Heartbeat monitor** (passive). Owns a unique ping URL. The monitored job calls it on completion. Configured with a *schedule* and a *grace period*.
- **HTTP monitor** (active). ping requests a target URL on an *interval* and evaluates assertions (status code, optional keyword).

**Schedule** — for heartbeat monitors, either a simple period ("every 15 minutes", "every 1 day") or a cron expression + timezone ("0 4 * * * Europe/Berlin").

**Grace period** — how late a check-in may be before the monitor is considered down. Default: 25% of the period (min 1 minute).

**Monitor states:**

| State | Meaning |
|---|---|
| `new` | Created, never pinged/probed yet. Never alerts. |
| `up` | Last check-in on time / last probes passing. |
| `late` | Heartbeat only: period elapsed, still within grace. No alert yet. |
| `down` | Grace exceeded, explicit `/fail` received, or probe failures confirmed. Alerts fire on entry. |
| `paused` | Manually paused. Check-ins still recorded; no state evaluation, no alerts. |

(`paused` is a presentation-level state: the schema stores it as a `paused_at` flag alongside the underlying lifecycle state, so resuming restores the prior state — see TECH-PLAN.md §2.3.)

**Event** — an immutable record of anything notable: state transitions, check-ins, probe failures, alerts sent, config changes. Powers the activity feed and audit trail.

## 6. User stories

- As an operator, I create a heartbeat monitor for my nightly backup, add `curl -fsS https://ping.mydomain.com/p/<slug>` as the last line of the script, and get an email if the backup doesn't run by 04:30.
- As an operator, my job can signal failure explicitly (`/p/<slug>/fail`) or report runtime by pinging `/p/<slug>/start` before and `/p/<slug>` after, so I can see how long backups take.
- As an operator, I add an HTTP monitor for `https://myapp.com` checked every minute, and get an email within ~3 minutes of it going down — and another when it recovers.
- As an operator, I open the dashboard and see every monitor's status, last check-in, and 90-day uptime at a glance, ordered so problems surface first.
- As an operator, I pause a monitor during a planned migration and resume it after, without deleting history.
- As a script author, I manage monitors via a REST API with an API key, so provisioning is automatable.

## 7. Functional requirements

### 7.1 Heartbeat monitors

- **F1.1** Each monitor gets an unguessable slug (≥ 16 random URL-safe chars). Ping endpoints require no auth — possession of the slug is the credential.
- **F1.2** Ping endpoints, all accepting `GET`, `POST`, and `HEAD`, always returning `200 OK` with a tiny body:
  - `POST/GET /p/<slug>` — success check-in.
  - `/p/<slug>/start` — job started (enables runtime measurement; also arms a "started but never finished" timeout).
  - `/p/<slug>/fail` — explicit failure → immediate `down`.
  - `/p/<slug>/<exit-code>` — exit-code style: `0` = success, non-zero = failure (enables `curl .../p/<slug>/$?`).
- **F1.3** Check-in ingestion records: timestamp, kind (success/start/fail), source IP, user agent, and up to 10 KB of request body (for log snippets). Bodies beyond the cap are truncated, not rejected.
- **F1.4** Schedules: simple period (1 min – 365 days) **or** cron expression (standard 5-field syntax) + IANA timezone. Grace period configurable (1 min – 30 days).
- **F1.5** State evaluation runs continuously (see NFRs): `up → late` at deadline, `late → down` at deadline + grace. A `down` monitor returns to `up` on the next successful check-in.
- **F1.6** Pinging a `paused` monitor records the check-in and (configurable, default on) auto-resumes it.

### 7.2 HTTP monitors

- **F2.1** Config: target URL (http/https), method (`GET`/`HEAD`), interval (30s – 24h, default 60s), timeout (1–30s, default 10s), expected status (default "2xx/3xx"), optional keyword that must (or must not) appear in the body, follow-redirects toggle, optional request headers (e.g. auth token).
- **F2.2** Failure confirmation: a monitor enters `down` only after **N consecutive failures** (default 2, configurable 1–10). Recovery requires 1 success. This is the anti-flap mechanism.
- **F2.3** Every probe records: timestamp, outcome, HTTP status, total latency (ms), and error string on failure. Latency feeds the response-time chart.
- **F2.4** TLS: probes to `https://` targets record certificate expiry; a warning event (and email, default on) fires 14 days before expiry.
- **F2.5** SSRF protection: target URLs are resolved and rejected if they point at private/loopback/link-local ranges (configurable allowlist override for genuinely internal self-host use).

### 7.3 Alerting (email, v1)

- **F3.1** Alerts fire on state transitions only: `* → down` (with reason: missed schedule, explicit fail, N probe failures + last error) and `down → up` (with downtime duration).
- **F3.2** Emails are plain, fast to scan: monitor name, state, reason, timestamp, direct dashboard link. Subject: `[DOWN] nightly-backup — missed check-in` / `[UP] nightly-backup — recovered after 42m`.
- **F3.3** Optional reminder while a monitor stays down (default: daily; configurable off/hourly/daily).
- **F3.4** Per-monitor alert mute, separate from `paused`.
- **F3.5** Delivery via SMTP (env-configured). Failures are retried with backoff (≥ 3 attempts) and recorded as events, so a lost alert is at least visible in the UI.
- **F3.6** The channel model is a `channels` abstraction with one implementation (email) — Slack/webhooks slot in at v2 without schema changes.

### 7.4 Dashboard (see DESIGN.md for full UX spec)

- **F4.1** Monitor list: status, name, kind, schedule/interval summary, last check-in (relative), 90-day uptime bar, latency sparkline (HTTP monitors). Problems sort to the top by default; search and kind/state filters.
- **F4.2** Monitor detail: current state + since-when, config summary, uptime % (7/30/90d), event feed, check-in log with bodies (heartbeat) or probe log with latency chart (HTTP), copy-paste snippets (`curl`, crontab line) for heartbeat monitors.
- **F4.3** Create/edit as a focused form with live preview of the schedule in human words ("expects a ping every day at 04:00 Berlin time; alert if 30 min late").
- **F4.4** Live-ish dashboard: monitor list refreshes automatically (polling is fine, ≤ 30s staleness).
- **F4.5** Empty, loading, and error states designed — never a blank white page.

### 7.5 Auth, account, API

- **F5.1** Email + password auth, JWT RS256 with httpOnly refresh cookies — reuse the shrt implementation. Single-user by default; registration can be disabled by env after first user (`REGISTRATION_OPEN=false`).
- **F5.2** API keys (create/revoke in settings) for the management REST API.
- **F5.3** REST API `/api/v1/...` covering monitor CRUD, pause/resume, listing check-ins/events. Documented in `docs/API.md` + `openapi.yaml`.
- **F5.4** Rate limiting: per-IP on ping endpoints (generous — bursts of legitimate pings must pass), per-user/key on the management API. Reuse the shrt Redis limiter.

### 7.6 Operations

- **F6.1** `/health` endpoint (DB + Redis + scheduler heartbeat). The monitoring service must itself be monitorable.
- **F6.2** Config exclusively via env vars, documented in `.env.example`; refuse to start on missing required config.
- **F6.3** Docker-compose for dev and prod, migrations via `make migrate-up`, single Go binary + Next.js app deployment shape (same as shrt).
- **F6.4** Data retention: raw check-ins/probe results pruned after 90 days (configurable); daily rollups (uptime %, latency percentiles) kept indefinitely for the long-range charts.

## 8. Non-functional requirements

- **N1 — Detection latency.** A monitor is marked `down` within 60s of its grace deadline; alert email is handed to SMTP within a further 30s.
- **N2 — Ingest performance.** Ping endpoint p99 < 50ms (excluding client network); check-in writes must not block on state evaluation.
- **N3 — Scheduler correctness.** Evaluation must be driven by DB state (e.g. indexed `next_deadline` scan), not in-memory timers, so restarts and crashes never lose a deadline. Exactly-one-alert per transition enforced transactionally.
- **N4 — Scale envelope.** Comfortable at 500 monitors / 1-minute intervals / 90 days of history on a $6 VPS (2 vCPU, 2 GB). Not a horizontal-scale design; document the envelope.
- **N5 — Security.** OWASP-conscious (reuse the security checklist approach from scrt): unguessable slugs, SSRF guard (F2.5), rate limits, no secrets in logs, HSTS in production. Ping check-in bodies are treated as untrusted content (rendered escaped, never executed).
- **N6 — Testing bar.** Unit tests for state machine + cron math (table-driven, timezone edge cases incl. DST), integration tests for scheduler and alert dedup, Playwright E2E for the critical path: register → create heartbeat monitor → ping it → see `up` → miss deadline (time-warped) → see `down` + alert event.
- **N7 — Accessibility & performance.** WCAG 2.1 AA (status never conveyed by color alone), Lighthouse ≥ 90 on dashboard.
- **N8 — Docs.** README with screenshots + quick start, `docs/ARCHITECTURE.md` (scheduler design gets its own diagram), `docs/API.md`, `CONTRIBUTING.md` — the shrt documentation set.

## 9. Data model (sketch — refined in the tech plan)

`users` · `api_keys` · `monitors` (kind, slug, schedule fields, grace, state, next_deadline, confirmation threshold…) · `checkins` (heartbeat traffic) · `probe_results` (HTTP traffic) · `events` (immutable feed) · `daily_stats` (rollups). Redis: rate limits, dashboard cache, scheduler lease/locks.

## 10. Success metrics

- Fresh-machine setup to first monitored cron job in ≤ 10 minutes.
- Zero missed and zero duplicate alerts in a 48h synthetic soak test (flaky target + chaos restarts of the app).
- E2E suite covers the critical path above and passes in CI.
- Design bar: dashboard screenshot is portfolio-lead material (per `DESIGN.md`), Lighthouse ≥ 90.

## 11. Milestones

| # | Scope | Exit criteria |
|---|---|---|
| M1 | Skeleton + heartbeat core | Repo scaffolding (shrt-style), auth, monitor CRUD, ping ingestion, state machine + scheduler, events. State machine fully unit-tested. |
| M2 | Alerting + dashboard | SMTP alerts with dedup + recovery, monitor list/detail with real data, uptime bars. |
| M3 | HTTP monitors | Prober with confirmation logic, latency capture + chart, TLS expiry warnings, SSRF guard. |
| M4 | Polish + ship | Design pass per DESIGN.md, E2E suite, docs + screenshots, soak test, `v1.0` tag. |

## 12. Risks

| Risk | Mitigation |
|---|---|
| Scheduler drift / missed deadlines after crash | DB-driven deadlines (N3); scheduler heartbeat exposed in `/health`; soak test with restarts. |
| Alert flapping erodes trust | Confirmation threshold (F2.2), state-transition-only alerts (F3.1), grace periods. |
| Self-host SMTP misconfiguration = silent failure | "Send test email" button in settings; alert delivery failures surfaced as events (F3.5). |
| Cron/timezone edge cases (DST) | Use a proven cron library; table-driven tests across DST transitions (N6). |
| Scope creep toward SaaS | Non-goals section is the contract; v2 list below. |

## 13. v2 candidates (explicitly out of v1)

Slack + generic webhook channels · public status pages · TCP/ICMP/DNS checks · multi-region probes · teams/roles · maintenance windows · Prometheus metrics endpoint · badge embeds (`![up](...)`).
