# Contributing

## Setup

```
git clone <repo> && cd ping
cp .env.example .env
make hooks
make docker-up
make migrate-up
make dev
```

See `docs/DEVELOPMENT.md` for details, including first-user bootstrap when `REGISTRATION_OPEN=false`.

## Workflow

- One ticket (`PING-XXX`) per branch: `ping-xxx-short-slug`, branched from `main`.
- Read the ticket's section in `TECH-PLAN.md` §8 fully before implementing; its acceptance criteria are the definition of done.
- Don't expand scope beyond the ticket — new ideas become new issue proposals.
- Commits follow [Conventional Commits](https://www.conventionalcommits.org/): `type(scope): summary`.
  - Types: `feat fix docs test refactor perf build chore`
  - Scopes: `server store worker db schedule alert fe docs infra`
  - Footer: `Refs: PING-XXX`
- PRs are squash-merged; the squash title must itself be a valid conventional commit.

## Quality gate

CI is currently offline, so **the gate is local and mandatory**: `make hooks` installs [lefthook](https://github.com/evilmartians/lefthook), which runs:

- **commit-msg** — rejects non-conventional commit messages
- **pre-commit** — gitleaks, `gofmt`, fast Go lint, frontend lint (staged files only, target < 10s)
- **pre-push** — `make verify` (target < 3 min)

`--no-verify` pushes are treated as broken builds and will be reverted. If a hook fails, fix the underlying issue — do not bypass it.

Tickets touching the database or workers also require `make test-integration` (needs `make docker-up`); UI-flow tickets require the Playwright suite (`cd frontend && npm run e2e`). Paste the `make verify` summary line and check off the PR template before requesting review.

## Code standards

Backend, database, and frontend conventions are enforced by the skills in `.claude/skills/` and pinned project-specifically in `TECH-PLAN.md` §5. Read those before writing code — they're the law, not a suggestion.
