<div align="center">

# ⌁ ping

**Know when it didn't run.**

A self-hostable cron-job + uptime monitor. Your services hit `ping.yourdomain.com/p/<slug>`
on a schedule — if they miss a check-in, you get an email. Also probes external URLs
with active HTTP checks.

</div>

---

> 🚧 **Under development.** v1 is being built ticket-by-ticket — see the issues tab.

| Doc | Contents |
|---|---|
| [PRD.md](PRD.md) | Product requirements — what we're building and why |
| [TECH-PLAN.md](TECH-PLAN.md) | Architecture, standards, quality gates, and the full ticket breakdown |
| [DESIGN.md](DESIGN.md) | Design system and screen specs ([live mockup](design-mockup.html)) |

**Stack:** Go 1.26 · chi · pgx/sqlc · Postgres · Redis · Next.js 16 · Tailwind v4 · shadcn/ui

The real README lands with `PING-024` when there's something to screenshot.

MIT © Ahmed Sukhera
