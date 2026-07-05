# Soak/chaos harness (PING-023)

Runs an isolated copy of the stack, creates a mix of synthetic heartbeat and
HTTP monitors, randomly restarts Postgres/Redis/the app binary for the
duration of the run, then audits the database for the invariants that matter:
no missed alerts, no duplicate alerts, no monitor stuck in `late`.

## Quick start

```
make soak                              # full 48h run, 10 monitors (the default)
SOAK_DURATION=10m MONITOR_COUNT=4 ./hack/soak/run.sh   # quick smoke run
```

Everything is isolated from your dev stack: a separate `docker compose`
project (`ping-soak`), separate Postgres/Redis ports (`15432`/`16379`), a
separate app port (`18080`), and a fresh JWT keypair — running this never
touches your `ping-dev` containers, database, or `.env`.

## What it does

1. Starts Postgres + Redis under the `ping-soak` compose project (ephemeral
   volumes distinct from dev's).
2. Runs migrations, generates a throwaway JWT keypair.
3. Builds four binaries fresh: the app (`cmd/ping`, untagged — real wall-clock
   time, not the e2e time-warp build), `soakloadgen`, `soakflakytarget`,
   `soakaudit`.
4. Starts one `soakflakytarget` instance per HTTP monitor (each flips its own
   `/health` between 200 and 503 on an independent random timer).
5. Starts the app under a respawn supervisor loop, so a chaos-triggered
   `SIGTERM` gets replaced rather than ending the run.
6. Starts `soakloadgen`, which registers a test user, creates the monitor mix
   (half heartbeat at the 60s period/grace floor, half HTTP at the 30s
   interval floor — packing the maximum number of state transitions into the
   run), then pings the heartbeat monitors continuously (occasionally skipping
   a ping or sending an explicit failure).
7. Starts the chaos loop (`chaos.sh`): every 2–10 minutes (configurable), picks
   one of restart-postgres / restart-redis / restart-app / no-op. Postgres and
   Redis are never restarted in the same action, so the app always has at
   least one dependency to reconnect to.
8. Sleeps for `SOAK_DURATION`, then stops the chaos loop and load generator.
9. Runs `soakaudit` against the live database and writes `report.md`.
10. Leaves the stack running for manual inspection — it does **not**
    `docker compose down` for you.

## Reading the report

`soakaudit` checks three invariants (see `backend/cmd/soakaudit/main.go` for
the exact queries):

- **Every down transition got exactly one non-reminder alert row that
  eventually resolved** (`status IN ('sent', 'failed')`, not stuck
  `'pending'`). A down event's alert is allowed to still be `pending` if the
  event is very recent (default: within the last 5 minutes) — the alerter
  simply hasn't ticked yet, that's not a miss.
- **No monitor stuck in `late` more than 2 minutes past its down-threshold
  deadline.**
- **No two consecutive same-type transitions** (`down, down` or `up, up`)
  with no opposite-type event between them, per monitor.

`PASS: all invariants held` with a summary line means the run is clean. A
`FAIL` prints every offending row — cross-reference the timestamp against
`chaos.log` to see which chaos action (if any) coincided with it.

## Artifacts

Each run gets its own directory: `hack/soak/run-<UTC timestamp>/`:

- `app.log` — the app's stdout/stderr across every respawn
- `loadgen.log`, `flakytarget.log` — synthetic traffic generator logs
- `chaos.log` — every chaos action and app respawn, with UTC timestamps
- `docker.log` — compose output
- `report.md` — the final invariant audit
- `keys/` — the throwaway JWT keypair for this run

## Tearing down

```
docker compose -p ping-soak -f docker-compose.yml -f hack/soak/docker-compose.soak.yml down -v
```

Also remove old `hack/soak/run-*/` directories once you're done inspecting
them — they aren't cleaned up automatically and aren't gitignored-by-content
(each run's binaries and keys are local artifacts, not committed).

## Notes

- No SMTP is configured for the soak run, so every alert takes the alerter's
  "no channel configured" path and resolves to `status = 'failed'`
  immediately — this is enough to satisfy the "≥ sent-or-failed" invariant
  without depending on real mail delivery.
- `MONITOR_COUNT` must be at least 2 (it's split evenly into heartbeat and
  HTTP monitors).
- Run duration accepts Go duration syntax (`48h`, `90m`, `5m30s`).
