# Changelog

All notable changes to ping. Format follows [Keep a Changelog](https://keepachangelog.com/);
versioning follows [SemVer](https://semver.org/).

## [1.0.0] — 2026-07-07

First release. Everything below was built ticket-by-ticket (PING-001…025 in
[TECH-PLAN.md §8](TECH-PLAN.md)).

### Added

- **Heartbeat monitors** — unguessable ping URLs; `GET/POST/HEAD` check-ins with
  body capture (10 KB, truncated never rejected); `/start`, `/fail`, and
  exit-code endpoints; simple periods or 5-field cron with IANA timezones and
  DST-correct math; configurable grace periods; pause/resume with auto-resume on ping.
- **HTTP monitors** — interval probes with status/keyword assertions, custom
  headers, redirect control, per-probe timeouts; N-consecutive-failure
  confirmation (anti-flap); latency recording with p50/p95 series; TLS
  certificate expiry warnings (14 days, re-armed on renewal); dial-time SSRF
  guard against private/loopback/metadata targets.
- **Alerting** — SMTP email on state transitions only (down with reason, up with
  downtime duration), optional reminders while down, per-monitor mute;
  transactional outbox with backoff retries — exactly-one-alert per transition,
  crash-safe.
- **Dashboard** — problem-first monitor list with 90-day uptime bars, stat
  strip, filters in URL state; monitor detail with check-in/probe logs, latency
  chart, event feed, copy-paste curl/crontab snippets; live schedule preview in
  the create/edit form; dark-first design per DESIGN.md.
- **Auth & API** — JWT RS256 with rotating httpOnly refresh cookies,
  registration lockdown (`REGISTRATION_OPEN`), hashed API keys, full REST API
  with OpenAPI 3.1 spec and cursor pagination.
- **Operations** — single binary with `--role api|worker|all`; DB-driven workers
  (`FOR UPDATE SKIP LOCKED`) for scheduler/prober/alerter/rollup; nightly
  rollups + 90-day retention pruning; `/health` with component + worker
  heartbeat checks; docker-compose for dev and prod.
- **Quality** — race-enabled unit suite, integration suite against real
  Postgres/Redis, Playwright E2E with a build-tagged test clock (compiled out of
  production), lefthook gates (gitleaks, conventional commits, `make verify`),
  path-filtered parallel CI.
