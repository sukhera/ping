# ping — agent instructions

`ping` is a self-hostable cron-job + uptime monitor. Go 1.26 + chi + pgx/sqlc + Postgres + Redis backend, Next.js 16 + Tailwind v4 + shadcn frontend.

## Source-of-truth documents (read before implementing anything)

- `PRD.md` — what we're building and why; scope boundaries (§3 non-goals are a contract)
- `TECH-PLAN.md` — architecture, folder structure, standards, and the ticket definitions (§8)
- `DESIGN.md` — design tokens, screens, components; `design-mockup.html` is the visual reference
- `docs/` — API/architecture/development docs, kept current as you build

## Ticket workflow

1. Work on exactly ONE ticket (PING-XXX) per branch: `ping-xxx-short-slug`.
2. Read the ticket's section in TECH-PLAN.md §8 fully — its acceptance criteria are the definition of done, plus §9.
3. Before writing any code, invoke every matching skill in `.claude/skills/` (golang-backend-specialist, database-specialist, security-specialist, react-frontend-specialist) via the Skill tool for the areas the ticket touches — e.g. a ticket adding a worker that makes outbound HTTP calls touches Go AND security, so both get invoked. This is a required step, not background context to keep in mind.
4. Plan before implementing on `size:L` tickets. Do not expand scope; new ideas become new issue proposals.

## Quality gate (CI is offline — this IS the gate)

- `make verify` must pass before any push. Never use `git commit --no-verify` or `git push --no-verify`.
- Tickets touching DB/workers also require `make test-integration`; UI flows require the Playwright suite.
- Conventional Commits enforced by hook: `type(scope): summary` — scopes: server store worker db schedule alert fe docs infra. Footer: `Refs: PING-XXX`.

## Hard rules

- Never commit: `.env`, `backend/keys/`, real secrets of any kind. `.env.example` gets placeholders only.
- `backend/db/*.go` is sqlc output: never hand-edit; change `db/queries/*.sql` then `make sqlc`.
- Migrations are immutable once merged; every migration has a tested `down`.
- Frontend: colors/typography ONLY via the DESIGN.md §4–5 tokens in `globals.css`. Raw hex in a component = blocker. Status always via `<StatusChip>` (shape + color + label).
- Package direction: `server → store → db`; `worker/* → store, schedule, alert`; `schedule` is pure (zero I/O). Nothing imports `server`.
- Workers claim work from Postgres with `FOR UPDATE SKIP LOCKED`; state transition + event + alert row commit in one transaction. Never in-memory timers per monitor.

## Commands

```
make dev / docker-up / migrate-up / migrate-down / sqlc / hooks
make verify            # full local gate (backend + frontend + generated-code drift)
make test-integration  # needs docker-up
cd frontend && npm run e2e
```
