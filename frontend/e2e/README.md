# E2E suite

Playwright's `webServer` auto-start isn't used here because these tests need a real backend (Postgres + Redis + the Go API), not just the frontend dev server — Playwright can only manage one process tree.

Before running `npm run e2e`, start both stacks manually from the repo root:

```
make docker-up
make migrate-up
make dev          # backend API, or: cd backend && go run ./cmd/ping --role=api
```

And in `frontend/`:

```
npm run dev
```

Then, from `frontend/`:

```
npx playwright install   # one-time browser download
npm run e2e
```

Tests self-register a unique timestamped email per run (`REGISTRATION_OPEN=true` is the `.env.example` default and there is no seed script — see `docs/DEVELOPMENT.md`).

## Time-warp specs (heartbeat-lifecycle, http-monitor-lifecycle)

These two specs call `POST /test/advance-clock` to cross real heartbeat grace deadlines and HTTP probe intervals instantly. That endpoint only exists in a backend built with `-tags e2e`, so `make dev`'s plain binary won't serve it — build and run the tagged binary instead of step 3 above:

```
make build-e2e
cd backend && PING_ENV=test PING_TEST_CLOCK=1 SSRF_ALLOWLIST=127.0.0.1/32,::1/128 ./tmp/ping-api-e2e --role=api
```

`SSRF_ALLOWLIST` is only needed for `http-monitor-lifecycle.spec.ts`, which points an HTTP monitor at a mock target the test spins up on `127.0.0.1` — the SSRF guard (`backend/worker/prober/probe.go`) blocks loopback by default, so without the allowlist every probe against it fails as "blocked" rather than exercising a real down/up transition.

See `docs/DEVELOPMENT.md`'s "Time-warp testing" section for how the endpoint works and why it's gated the way it is.
