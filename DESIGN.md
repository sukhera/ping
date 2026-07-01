# ping — Product Design Specification

**Version:** 1.0 · **Date:** 2026-07-01 · Companion to `PRD.md` · Mockup: `design-mockup.html`

---

## 1. Where shrt and scrt fall short (design review)

Both apps are *clean*, but neither is *designed*. Concretely:

1. **Stock shadcn identity.** Both ship the default shadcn/ui token set with exactly one change — the primary hue (violet 262° in shrt, emerald 158° in scrt). Same zinc neutrals, same `0.5rem` radius, same default spacing. Anyone who has scaffolded shadcn recognizes it in two seconds; the apps read as "template" rather than "product."
2. **No signature element.** Nothing on screen is ownable. shrt's dashboard is a plain table; scrt's create form is a plain card. There is no visual idea a screenshot could be remembered by.
3. **Accent color is decorative, not semantic.** The violet/emerald is applied to buttons and links but doesn't *mean* anything. In a monitoring product especially, color must carry state.
4. **Light-first default** reads generic for developer tools. Both support dark mode, but as an inversion of a light design rather than a designed surface.
5. **Typography does no work.** One size ramp, no tabular figures, minimal use of monospace despite both products being about URLs and keys — exactly the content mono is for. (scrt loads Geist Mono but barely uses it.)
6. **No portfolio coherence.** Two frontends (Next vs Vite), two accent hues, no shared visual thread. Fine individually; a missed opportunity collectively.

None of this is fatal for shrt/scrt — but ping is a *status* product, where design is the product. The spec below fixes all six points, and §10 notes what can be back-ported.

## 2. Design principles

1. **Status is the hero.** Every screen answers "is everything OK?" before anything else. State color, not brand color, dominates.
2. **Calm by default, loud when down.** An all-green dashboard should feel almost boring — quiet, dark, low-contrast. A `down` monitor should be impossible to miss.
3. **Dense, not cramped.** Operators scan lists. Favor one information-rich row per monitor over spacious cards.
4. **Mono is the brand.** Slugs, timestamps, latencies, cron expressions — the product's nouns are machine strings. Set them all in monospace and make that the visual signature.
5. **Motion means liveness.** The only animations are ones that signal the system is alive (pulse on fresh check-in, drawing sparklines). No decorative motion.

## 3. Brand

- **Name:** always lowercase — `ping`.
- **Logomark:** a heartbeat/pulse glyph — a flat line with a single sharp spike (⌁). Doubles as favicon, empty-state art, and the `up` animation motif.
- **Voice:** terse, operator-grade. "Missed check-in. Expected 04:00, last seen 03:12 yesterday." No exclamation marks, no "Oops!".
- **Tagline:** "Know when it didn't run."

## 4. Color system

Dark-first. The dark theme is the designed artifact; light is derived and secondary.

### Surfaces & text (dark)

| Token | Value | Use |
|---|---|---|
| `--bg` | `#0B0D10` | App canvas (near-black, blue-cast) |
| `--surface` | `#12151A` | Cards, sidebar |
| `--surface-2` | `#1A1E25` | Hover, inputs, nested panels |
| `--border` | `#232830` | Hairlines (1px, low contrast) |
| `--text` | `#E6EAF0` | Primary text |
| `--text-dim` | `#8B94A3` | Secondary text, labels |
| `--text-faint` | `#5C6470` | Timestamps, placeholders |

### Semantic status (the real palette)

| Token | Value | Meaning |
|---|---|---|
| `--up` | `#2DD4A7` | Up / passing (mint-teal — deliberately not scrt's emerald) |
| `--late` | `#F5B84B` | Late / warning |
| `--down` | `#F4564E` | Down / failing |
| `--paused` | `#5C6470` | Paused (drops to gray — visually leaves the game) |
| `--new` | `#6E9BF5` | New / never pinged |

Rules: status colors are reserved for status — never for buttons or links. Each status pairs a color with a **shape** (dot=up, hollow ring=paused, triangle=late, square=down) and a **label**, so state survives color-blindness and grayscale (WCAG 1.4.1). `--down` additionally gets a soft ambient glow (`box-shadow: 0 0 24px rgba(244,86,78,.25)`) — the one place the calm surface is allowed to shout.

### Action color

`--accent: #6E9BF5` (desaturated cornflower) for buttons, links, focus rings. Intentionally quiet so green/amber/red keep full semantic force. This also gives the portfolio a third hue (shrt violet · scrt emerald · ping blue) under one shared neutral system.

### Light mode

Same tokens remapped (`--bg #F7F8FA`, `--surface #FFFFFF`, status hues darkened ~15% for contrast). Ships at v1 but marketing/screenshots use dark.

## 5. Typography

| Role | Face | Notes |
|---|---|---|
| UI | **Geist Sans** | 400/500/600. Shared with scrt — the portfolio thread. |
| Data | **Geist Mono** | Slugs, URLs, cron expressions, timestamps, latencies, uptime %, log bodies. Always with `font-variant-numeric: tabular-nums` so columns of numbers align. |

Scale (rem): 0.75 (labels, overline) · 0.8125 (table meta) · 0.875 (body/rows) · 1.0 (section) · 1.25 (page title) · 2.0 (big status numerals). Row text is deliberately small; density principle.

## 6. Layout & navigation

App frame: fixed left sidebar (220px, `--surface`) + fluid content (max 1200px).

Sidebar top→bottom: logomark + `ping` · **global status summary** ("12 up · 1 down" with dots — visible on every screen) · nav (Monitors, Events, Settings) · user/theme footer.

Content header per page: title, filter controls, primary action (`+ New monitor`). No topbar; vertical rhythm on an 8px grid, hairline borders instead of shadows (shadows reserved for the `down` glow and overlays).

## 7. Key screens

### 7.1 Dashboard (monitor list) — the money screen

One row per monitor, columns:

`[status shape+dot] [name + slug·mono] [kind chip] [schedule summary] [last check-in, relative] [90-day uptime bar] [uptime % / latency sparkline] [⋯ menu]`

- **Sort:** problems first (`down` → `late` → `new` → `up` → `paused`), then by name. A red row physically floats to the top.
- **90-day uptime bar** — the signature element (see §8).
- Rows are fully clickable → detail. Hover raises to `--surface-2`.
- Top strip above the list: three big-numeral stat blocks — Up / Down / Late counts (mono, 2rem) — a glanceable answer before a single row is read.
- **Filters:** text search, kind (heartbeat/HTTP), state. Persisted in URL params.
- **Empty state:** pulse glyph, "No monitors yet.", inline copy-paste example (`curl https://…/p/demo`) and the `+ New monitor` button. Never a blank page.

### 7.2 Monitor detail

Header: status shape + name + big state word ("UP since Jun 12, 04:00"), pause/mute/edit actions.
Then a stat row: uptime 7/30/90d · avg latency (HTTP) or avg runtime (heartbeat with `/start`) · total check-ins.
Then, by kind:

- **Heartbeat:** "How to ping" panel with copy-paste `curl` line and a crontab example rendered in mono; check-in log (time, kind, source IP, body preview — expandable, always escaped); event feed.
- **HTTP:** latency chart (area, 24h/7d/30d toggles, `--up` fill fading to transparent; failures as `--down` dots on the line); probe log; TLS expiry note ("cert expires in 41 days").

The 90-day bar appears full-width here, each cell hoverable → per-day tooltip (uptime %, incidents).

### 7.3 New / edit monitor

Single centered column (max 560px), progressive disclosure: kind toggle first (two fat radio cards: ⌁ Heartbeat / ⇄ HTTP check), then only the relevant fields.

The signature detail: a **live plain-language schedule preview** in an `--surface-2` panel that updates as you type — "Expects a ping **every day at 04:00** (Europe/Berlin). Alerts if **30 min** late." Cron expressions get inline validation with next-3-runs preview. Advanced fields (headers, confirmation threshold, body keyword) behind a "Advanced" disclosure.

### 7.4 Events page

Global reverse-chron feed, one line per event: time (mono) · status shape · monitor name · message. Filter by monitor/type. Doubles as the audit trail (F4 in PRD).

### 7.5 Settings

Tabs: Account · API keys (mono keys, shown once, revoke) · Alerting (SMTP status, **"Send test email"** button, reminder cadence) · Appearance.

### 7.6 Auth screens

Minimal centered card on `--bg`, logomark above. No marketing chrome — this is a tool, not a landing page. (A public landing page is out of v1 scope.)

## 8. Signature element: the uptime bar

90 cells (1 day each, most recent right), 3×14px rounded rects with 2px gaps: `--up` fill at 60% opacity for clean days, `--late`/`--down` at full opacity for degraded/incident days, `--border` for no-data. Instantly legible, information-dense, and screenshot-defining — the ping equivalent of GitHub's contribution graph. Rendered as inline SVG (no chart lib needed for it).

Second motif: the **live pulse**. When a check-in arrives (or dashboard poll shows one since last render), the monitor's status dot emits one expanding ring (600ms ease-out, then done). Respects `prefers-reduced-motion`.

## 9. Components & implementation notes

- Base: shadcn/ui (New York) on Tailwind v4 — same toolchain as scrt — but **retokened** per §4/§5; radius 6px (`--radius: 0.375rem`), hairline borders, no default shadows.
- Charts: latency/sparklines via `recharts` (or hand-rolled SVG for sparklines); uptime bar always hand-rolled SVG.
- Tables are semantic `<table>` with sticky header; keyboard navigable (`j/k` row focus is a nice-to-have).
- Status chip is one shared component: shape + color + label, sized `sm` (rows) and `lg` (detail header).
- All timestamps: relative in rows ("3m ago"), absolute + TZ in tooltips and detail ("2026-07-01 04:00:12 UTC"), mono everywhere.

## 10. Accessibility & responsive

- WCAG 2.1 AA. Status = color + shape + text (never color alone). Contrast checked on dark surfaces (`--text-dim` on `--surface` ≥ 4.5:1).
- Focus rings: 2px `--accent`, visible on dark. Full keyboard path for CRUD.
- Responsive: sidebar collapses to icon rail < 1024px, bottom nav < 640px; monitor rows stack name/status above meta. Dashboard remains usable on a phone — checking "is it down?" from bed is a core use case.
- `prefers-reduced-motion`: pulse and chart-draw animations disabled.

## 11. Back-porting to shrt & scrt (later, cheap wins)

Adopt the §4 neutral surface system and Geist pairing in both (keeping their violet/emerald accents), use mono for slugs/URLs/keys, add each product's one signature element (shrt: click-count sparkline per link; scrt: the burn-on-read countdown moment), and unify radius/border treatment. ~1 day each; the portfolio then reads as a family.
