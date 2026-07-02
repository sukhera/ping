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
