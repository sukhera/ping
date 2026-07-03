# Architecture

> This document is seeded by PING-012 to record the alerting-delivery decisions
> its acceptance criteria require. PING-024 expands it into the full architecture
> reference (the §2 diagrams and the complete "Why the scheduler can't miss"
> walkthrough). What is here now is accurate; it is not yet exhaustive.

## The outbox: how a state change becomes a notification

Workers are **database-driven, not timer-driven**. There is no in-memory timer
per monitor. Two loops cooperate through Postgres:

1. The **scheduler** (`worker/scheduler`, PING-009) ticks every ~15s, claims
   heartbeat monitors whose deadline has passed with `FOR UPDATE SKIP LOCKED`,
   and — in **one transaction** — writes the state transition, the timeline
   `event`, and a **pending row in the `alerts` outbox table**. The ingest path
   (a failing/recovering check-in) does the same via `recordTransition`. Because
   the transition and its outbox row commit atomically, a crash before commit
   leaves no trace and the monitor stays due for the next tick: **no lost
   transition, no duplicate outbox row.**

2. The **alerter** (`worker/alerter`, PING-012) ticks every ~30s and drains that
   outbox: it claims due pending rows, sends each through an `alert.Channel`
   (SMTP email in v1), and records the outcome.

The outbox decouples *deciding to alert* (must be transactional and never
missed) from *delivering the alert* (slow, external, failure-prone, must be
retried) — the classic transactional-outbox pattern.

## Alerter delivery semantics

### Claim, then send (the lock is not held across SMTP)

`ClaimDueAlerts` selects due pending rows `FOR UPDATE OF a SKIP LOCKED`, but the
claiming transaction **commits immediately** — the row lock is released before
the (slow, external) SMTP round-trip. Holding a Postgres row lock open across a
20-second SMTP dial would tie up a connection and a lock for the duration of
every send. `SKIP LOCKED` still lets multiple alerter replicas claim disjoint
rows concurrently.

### At-most-once delivery (the accepted edge)

The sequence per alert is: **claim (commit) → `Send()` → mark `sent`.** If the
process is killed *after* SMTP accepts the message but *before* `MarkAlertSent`
commits, the row stays `pending` and would be re-claimed and **re-sent** on the
next tick — a duplicate email.

**Decision: we accept at-most-once and do not re-send in that window.** We do not
add an idempotency key. Rationale:

- Down/up/reminder alerts are **low-volume** and self-healing: a genuinely
  missed notification is followed by the next reminder (still-down monitors) or
  the next transition, so a dropped alert is not a silent permanent loss.
- SMTP has **no native dedup**; an idempotency key would only set a `Message-ID`
  and hope the receiving server collapses duplicates — weak and non-portable.
- The failure window is a hard kill in a sub-second gap. Weighed against the cost
  of duplicate-page fatigue, **dropping in that window is the better default.**

Concretely: on a retryable failure we bump `attempts` and reschedule; on success
we mark `sent`. We never resend a row we might have already delivered.

### Retry / backoff

A delivery failure is classified by `alert.SendError.Retryable` (transient
network / SMTP 4xx = retryable; SMTP 5xx and auth = permanent). Retryable
failures are rescheduled with escalating backoff and a bounded attempt budget:

| Attempt | Backoff before it | Outcome if it fails |
| --- | --- | --- |
| 1 | (immediate on claim) | reschedule +1m |
| 2 | 1m | reschedule +5m |
| 3 | 5m (…+25m would follow) | **terminal `failed`** |

After `maxAttempts` (3) or on any permanent error, the alert is marked `failed`
**and an `alert_failed` event is written in the same transaction**, so the
failure is visible on the monitor's timeline. A missing/unconfigured channel is
treated as permanent (fail fast rather than burn retries against a channel that
cannot come up at runtime).

### Mute

A muted monitor still records its state transitions and events (muting is not
pausing). But its outbox alert is **resolved without sending**: `SuppressAlert`
sets the row to the terminal `sent` status while leaving `sent_at` NULL, so it
leaves the pending outbox, is never retried, and never delivers an email.
Reminders are suppressed the same way — the reminder-enqueue query skips muted
monitors entirely.

### Reminders

While a monitor stays down, the alerter enqueues a "still down" reminder on a
**per-monitor cadence** (`monitors.reminder_every_s`, default 86400 = daily; 0
disables). `EnqueueDownReminders` inserts a fresh pending alert only when no
alert has been enqueued for that monitor within the cadence window. That single
predicate both enforces the cadence and prevents stacking duplicate reminders: a
reminder just inserted has `created_at ≈ now`, so it sits inside the window and
blocks the next enqueue until a full cadence elapses.

A reminder has no event of its own — it reuses the outage's originating `down`
event as its `event_id` (so it links back to the outage and downtime resolves
correctly). Because the event type is therefore `down`, the alerter cannot tell a
reminder from the original down alert by event type; instead the reminder row
carries `alerts.is_reminder = true`, and the alerter renders the reminder
template ("still down after 3h") when that flag is set and the down template
otherwise. Recovery ("recovered after 42m") and reminder ("still down for 3h")
durations are both measured from that originating `down` event.
