# Development

## First-time setup

```
git clone <repo> && cd ping
cp .env.example .env
make hooks       # installs lefthook (commit-msg, pre-commit, pre-push)
make docker-up   # Postgres 16 + Redis 7
make migrate-up  # no-ops until PING-002 adds migrations
make dev         # air, live-reloading backend/cmd/ping
```

### First-user bootstrap

`REGISTRATION_OPEN` in `.env` gates the register endpoint (see PING-004). Leave it `true` to create the first (and, for single-user self-hosting, only) account, then set it to `false` and restart to lock registration down. There is no separate seed/bootstrap script — the first successful registration *is* the bootstrap.

When `REGISTRATION_OPEN=false`, `POST /api/v1/auth/register` returns `403 Forbidden` with `{"error": "registration is closed"}`. The frontend should show a fixed, user-facing message rather than surfacing that string directly.

### Auth (JWT RS256)

`POST /api/v1/auth/{register,login,refresh,logout}` issue short-lived RS256 access tokens (returned in the response body, kept in memory client-side) plus a rotating refresh token in an httpOnly cookie (`ping_refresh`, scoped to `/api/v1/auth`). Refresh tokens rotate on every use; replaying an already-rotated token revokes its entire session family (theft protection). Login/register are rate-limited to 5 attempts/minute/IP (Redis, fails open if Redis is unreachable).

The API needs an RSA keypair at the paths configured by `JWT_PRIVATE_KEY_PATH` / `JWT_PUBLIC_KEY_PATH` (relative to `backend/`, the working directory the API process runs from). Generate a local dev keypair once:

```
mkdir -p backend/keys
openssl genrsa -out backend/keys/jwt_private.pem 2048
openssl rsa -in backend/keys/jwt_private.pem -pubout -out backend/keys/jwt_public.pem
```

`backend/keys/` is gitignored — never commit real keys. The server fails fast at startup if the configured paths don't resolve to a valid keypair.

### Frontend

```
cd frontend
echo "NEXT_PUBLIC_API_BASE_URL=http://localhost:8080" > .env.local
npm install
npm run dev      # http://localhost:3000, must match CORS_ALLOWED_ORIGIN in .env
```

Dark theme is the default (light/system also available via the sidebar theme toggle, persisted by `next-themes`). Design tokens live exclusively in `frontend/app/globals.css` (DESIGN.md §4-5) — raw hex colors elsewhere are rejected by an ESLint rule (`no-restricted-syntax` in `frontend/eslint.config.mjs`), enforced by the `fe-lint` pre-commit hook. The E2E suite (`frontend/e2e/`) needs the backend running too — see `frontend/e2e/README.md`.

shadcn components were generated with `components.json`'s `"style": "radix-nova"` rather than the classic `"new-york"` DESIGN.md/TECH-PLAN reference by name — the shadcn CLI (v4.12+) replaced those style names with a `radix|base` + preset taxonomy and no longer offers `new-york` as a value. Functionally equivalent for our purposes since every color/spacing token is retokened in `globals.css` regardless of preset.

## Make targets

| Target | Does |
|---|---|
| `make dev` | Runs the API + workers with live reload (`air`) |
| `make docker-up` / `docker-down` | Starts/stops Postgres + Redis via `docker-compose.yml` |
| `make migrate-up` / `migrate-down` | Applies/rolls back `backend/db/migrations` (golang-migrate) |
| `make sqlc` | Regenerates `backend/db/*.go` from `backend/db/queries/*.sql` |
| `make hooks` | Installs lefthook git hooks |
| `make tools` | Installs pinned golangci-lint/sqlc/migrate (versions shared with CI) |
| `make verify` | Full local gate: backend + frontend + generated-code drift — must pass before every push |
| `make test-integration` | Integration tests behind `-tags integration`; needs `make docker-up` |

## Quality gate

lefthook is the first, local gate — fast feedback before anything reaches GitHub:

- **commit-msg** — rejects non-Conventional-Commit messages
- **pre-commit** — gitleaks (staged secrets), `gofmt`, fast Go lint, frontend lint, staged files only
- **pre-push** — `make verify`

CI (`.github/workflows/ci.yml`) is the second enforcement point: path-filtered `backend`/`frontend`/`integration`/`e2e` jobs run per PR (see TECH-PLAN.md §6.6). `--no-verify` is never used; see `CONTRIBUTING.md`.

## Migrations & sqlc

Introduced in PING-002. Migrations live in `backend/db/migrations/` (golang-migrate, up+down, immutable once merged). Queries live in `backend/db/queries/*.sql`; `make sqlc` regenerates `backend/db/*.go`, which is committed and verified drift-free by `make verify` (`verify-generated`). Never hand-edit generated files — change the query, then regenerate.

## Time-warp testing

Two distinct mechanisms, at two different layers:

- **Unit/integration tests** (introduced with the `schedule` package, PING-006, and the scheduler worker, PING-009): deadline/grace-period math is tested by passing an explicit `now time.Time` into `store.EvaluateDueMonitors` and friends rather than sleeping — see `backend/store/scheduler_test.go`.
- **E2E time-warp endpoint** (PING-022): Playwright specs run against a real running backend over HTTP, so there's no `*testing.T` to inject a clock into. `POST /test/advance-clock` (`backend/server/testclock.go`) fills that gap for black-box tests:
  - Body: `{"seconds": <n>}`. Advances a process-wide skewed clock (`backend/internal/testclock`) and synchronously drives one settle-to-quiescence pass of the scheduler (looping `EvaluateDueMonitors` until a pass claims nothing, since each pass only crosses one threshold — up→late or late→down), then one prober pass, then one alerter pass — all using the new time. This makes it work identically whether or not `--role=worker` is also running (the e2e CI job runs `--role=api` only).
  - **Compiled out of every binary not built with `-tags e2e`** (`go build -tags e2e ./cmd/ping`, or `make build-e2e`) — `backend/server/testclock_notag_test.go` asserts a 404 without the tag. Even in a tagged binary, the route only registers when `PING_ENV=test` (`Deps.Env`), mirroring the auth-rate-limit escape hatch below — belt and suspenders, never reachable in dev or production.
  - Local manual e2e runs need the tagged binary: `make build-e2e` then run `backend/tmp/ping-api-e2e --role=api` with `PING_ENV=test PING_TEST_CLOCK=1 SSRF_ALLOWLIST=127.0.0.1/32,::1/128` (the allowlist is only needed for `http-monitor-lifecycle.spec.ts`, which probes a mock target on loopback — the SSRF guard blocks loopback by default). `frontend/e2e/README.md` has the full command.
  - Heartbeat and HTTP monitor minimums apply here too: `grace_s`/`period_s` floor is 1 minute (`schedule.MinGrace`/`MinPeriod`), `interval_s` floor is 30s — specs advance the clock by whole multiples of those, not arbitrary small numbers.

## E2E auth rate limit (read before adding a Playwright spec)

The auth endpoints are rate-limited to 5/min **per IP** (`authRateLimit`/`authRateWindow` in `backend/server/auth.go`), keyed separately for register and login. Every Playwright worker shares one localhost IP, so all spec files draw from the **same** 5/min bucket. Adding a spec that registers or logs in used to silently eat into that shared budget — once the suite crossed 5 registrations in a window, whichever call landed 6th got a 429, and the failure surfaced in an unrelated spec.

The fix (PING-014): the limiter is disabled in the e2e environment only. `main.go` sets `Deps.AuthRateLimitDisabled = cfg.Env == "test"` (mirroring how `CookieSecure` keys off `cfg.Env == "production"`), and `checkRateLimit` short-circuits when it's set. The CI e2e job already runs with `PING_ENV=test`, so specs can register/login freely there. **This escape hatch is test-only** — it must never be enabled in dev or production, where the limiter is a real abuse control.

Guidance for new specs: you no longer have to ration register/login calls, but still prefer sharing one registration/session across a `test.describe.serial` block where practical — it keeps specs fast and keeps the real limiter meaningfully exercised by the dedicated `TestRateLimit_*` unit tests rather than accidentally by e2e.

## Known gaps

- **Monitor detail stat row (PING-014):** DESIGN.md §7.2 specs "avg runtime
  (heartbeat with `/start`) · total check-ins" in the stat row. No ticket
  scoped a backend aggregate for either — PING-014 is `frontend`-only,
  depending only on PING-013. The frontend approximates both from data already
  available: total check-ins sums `daily_stats.checkins` over the fetched
  window (not a true all-time count once rows fall out of the retention
  window, PING-020), and avg runtime pairs `start`/`success` check-ins from
  the currently loaded page of the check-in log (not the monitor's full
  history). A future ticket should add a real backend aggregate if exact
  all-time figures are needed.
