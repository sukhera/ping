## What & why
Closes #XXX
Refs: PING-XXX

## Checklist
- [ ] `make verify` passes locally (paste the summary line)
- [ ] New/changed behavior covered by tests (unit; integration if it touches DB/workers)
- [ ] Migrations: up AND down tested (`make migrate-up && make migrate-down && make migrate-up`)
- [ ] No new colors/typography outside DESIGN.md tokens (frontend PRs)
- [ ] Docs updated (API.md / ARCHITECTURE.md / .env.example) if behavior or config changed
- [ ] Screenshots attached for UI changes (dark theme)
