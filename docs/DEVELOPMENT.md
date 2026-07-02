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

## Make targets

| Target | Does |
|---|---|
| `make dev` | Runs the API + workers with live reload (`air`) |
| `make docker-up` / `docker-down` | Starts/stops Postgres + Redis via `docker-compose.yml` |
| `make migrate-up` / `migrate-down` | Applies/rolls back `backend/db/migrations` (golang-migrate) |
| `make sqlc` | Regenerates `backend/db/*.go` from `backend/db/queries/*.sql` |
| `make hooks` | Installs lefthook git hooks |
| `make verify` | Full local gate: backend + frontend + generated-code drift — must pass before every push |
| `make test-integration` | Integration tests behind `-tags integration`; needs `make docker-up` |

## Quality gate

CI is offline, so lefthook is the real, machine-enforced gate:

- **commit-msg** — rejects non-Conventional-Commit messages
- **pre-commit** — gitleaks (staged secrets), `gofmt`, fast Go lint, frontend lint, staged files only
- **pre-push** — `make verify`

`--no-verify` is never used; see `CONTRIBUTING.md`.

## Migrations & sqlc

Introduced in PING-002. Migrations live in `backend/db/migrations/` (golang-migrate, up+down, immutable once merged). Queries live in `backend/db/queries/*.sql`; `make sqlc` regenerates `backend/db/*.go`, which is committed and verified drift-free by `make verify` (`verify-generated`). Never hand-edit generated files — change the query, then regenerate.

## Time-warp testing

Introduced alongside the `schedule` package (PING-006) and the scheduler worker (PING-009): deadline/grace-period math is tested by injecting a fake clock rather than sleeping in tests. Details land here once that package exists.
