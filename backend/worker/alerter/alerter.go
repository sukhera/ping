// Package alerter runs the outbox-dispatch loop: every tick it enqueues any due
// "still down" reminders, claims pending alert rows, and delivers each via the
// alert channel — marking it sent, backing it off for retry, or failing it after
// the retry budget is spent. Like the scheduler it is DB-driven (claims work
// from Postgres each tick) rather than timer-driven. Package direction:
// worker/alerter → store, alert.
package alerter

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/sukhera/ping/alert"
	"github.com/sukhera/ping/store"
	"github.com/sukhera/ping/worker"
)

const (
	// DefaultInterval is the production tick cadence. Delivery latency of a few
	// tens of seconds is fine for email alerts, and a slower tick keeps SMTP
	// round-trips and retry backoff from dominating the loop.
	DefaultInterval = 30 * time.Second

	// claimLimit caps alerts processed per tick so one slow SMTP server can't
	// make a single tick run unboundedly long; the next tick drains the rest.
	claimLimit = 100

	// maxAttempts is the total number of delivery attempts before an alert is
	// terminally failed. With the backoff schedule below this is ~31m of retries.
	maxAttempts = 3

	// baseBackoff is the first retry delay; subsequent retries multiply it
	// (1m → 5m → 25m) so a transiently-down mail server gets escalating breathing
	// room without pinning the worker.
	baseBackoff = time.Minute
	backoffMult = 5

	// sendTimeout bounds a single SMTP round-trip so a wedged mail server can't
	// stall the whole tick.
	sendTimeout = 20 * time.Second
)

// Store is the slice of *store.Store the alerter needs, as an interface so the
// loop can be unit-tested without Postgres.
type Store interface {
	EnqueueDownReminders(ctx context.Context, now time.Time) (int, error)
	ClaimDueAlerts(ctx context.Context, now time.Time, limit int32) ([]store.AlertJob, error)
	MarkAlertSent(ctx context.Context, id int64) error
	SuppressAlert(ctx context.Context, id int64) error
	RescheduleAlert(ctx context.Context, id int64, nextAttempt time.Time) error
	FailAlert(ctx context.Context, id int64, monitorID, reason string) error
	ResolveDowntime(ctx context.Context, monitorID string, recoveredAt time.Time) (time.Duration, error)
}

// Alerter delivers pending outbox alerts through ch, using st for claiming and
// state. baseURL is the dashboard origin used to build "View monitor" links.
type Alerter struct {
	st      Store
	ch      alert.Channel
	baseURL string
}

// New builds an Alerter. ch may be nil when SMTP is unconfigured; in that case
// every claimed alert is failed fast with a clear event rather than retried
// forever against a channel that cannot be configured at runtime. baseURL is the
// dashboard origin for "View monitor" links ("" omits them).
func New(st Store, ch alert.Channel, baseURL string) *Alerter {
	return &Alerter{st: st, ch: ch, baseURL: baseURL}
}

// Run starts the alerter loop and blocks until ctx is cancelled (graceful stop).
// interval is a parameter so integration tests can drive fast ticks; production
// passes DefaultInterval. baseURL is the dashboard origin for "View monitor"
// links.
func Run(ctx context.Context, st Store, ch alert.Channel, hb *worker.Heartbeat, interval time.Duration, baseURL string) error {
	return New(st, ch, baseURL).run(ctx, hb, interval)
}

func (a *Alerter) run(ctx context.Context, hb *worker.Heartbeat, interval time.Duration) error {
	return worker.Run(ctx, worker.Config{
		Name:     "alerter",
		Interval: interval,
		Jitter:   interval / 8,
		HB:       hb,
		Tick:     a.tick,
	})
}

// tick does one dispatch pass: enqueue due reminders, claim due alerts, deliver
// each. A per-alert failure is logged and skipped, never fatal to the loop.
func (a *Alerter) tick(ctx context.Context) error {
	if n, err := a.st.EnqueueDownReminders(ctx, time.Now()); err != nil {
		// A reminder-enqueue failure must not block dispatching already-pending
		// alerts, so log and continue rather than returning.
		slog.WarnContext(ctx, "alerter: enqueue reminders failed", "error", err)
	} else if n > 0 {
		slog.InfoContext(ctx, "alerter reminders queued", "count", n)
	}

	jobs, err := a.st.ClaimDueAlerts(ctx, time.Now(), claimLimit)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		a.dispatch(ctx, job)
	}
	return nil
}

// dispatch delivers one claimed alert and records its outcome. It never returns
// an error: a single alert's failure is recorded on that alert (retry or fail),
// not propagated to the tick, so one bad recipient can't stall the outbox.
func (a *Alerter) dispatch(ctx context.Context, job store.AlertJob) {
	log := slog.With("alert_id", job.ID, "monitor", job.MonitorSlug, "event", job.EventType)

	// Muted: the transition/event was already recorded upstream; we owe no
	// notification. Resolve the row so it leaves the outbox and isn't retried.
	if job.AlertsMuted {
		if err := a.st.SuppressAlert(ctx, job.ID); err != nil {
			log.WarnContext(ctx, "alerter: suppress muted alert failed", "error", err)
		}
		return
	}

	// No channel configured: fail fast with a clear event. Retrying would just
	// burn the attempt budget against a channel that can't come up at runtime.
	if a.ch == nil {
		a.fail(ctx, log, job, "email delivery is not configured")
		return
	}

	msg, err := a.render(ctx, job)
	if err != nil {
		// A render/data-resolution failure is our bug, not a transport blip, and
		// won't fix itself on retry — fail it so it's visible rather than looping.
		a.fail(ctx, log, job, "could not build notification")
		log.WarnContext(ctx, "alerter: render failed", "error", err)
		return
	}

	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	sendErr := a.ch.Send(sendCtx, msg)
	if sendErr == nil {
		if err := a.st.MarkAlertSent(ctx, job.ID); err != nil {
			log.WarnContext(ctx, "alerter: mark sent failed", "error", err)
		}
		return
	}

	// A retryable failure with budget left → back off; otherwise terminal fail.
	// ErrNotConfigured is treated as permanent (IsRetryable reports false).
	nextAttempt := job.Attempts + 1
	if alert.IsRetryable(sendErr) && nextAttempt < maxAttempts {
		delay := backoff(job.Attempts)
		if err := a.st.RescheduleAlert(ctx, job.ID, time.Now().Add(delay)); err != nil {
			log.WarnContext(ctx, "alerter: reschedule failed", "error", err)
			return
		}
		log.InfoContext(ctx, "alerter: delivery retry scheduled",
			"attempt", nextAttempt, "retry_in", delay, "error", sendErr)
		return
	}

	a.fail(ctx, log, job, "mail server rejected the message")
	log.WarnContext(ctx, "alerter: delivery permanently failed",
		"attempts", nextAttempt, "error", sendErr)
}

// fail terminally fails a job and records the visible "alert_failed" event.
func (a *Alerter) fail(ctx context.Context, log *slog.Logger, job store.AlertJob, reason string) {
	if err := a.st.FailAlert(ctx, job.ID, job.MonitorID, reason); err != nil {
		log.WarnContext(ctx, "alerter: mark failed", "error", err)
	}
}

// render builds the channel-ready Message for a job, choosing the notification
// kind from the triggering event type and resolving recovery downtime.
func (a *Alerter) render(ctx context.Context, job store.AlertJob) (alert.Message, error) {
	n := alert.Notification{
		MonitorName:  job.MonitorName,
		MonitorSlug:  job.MonitorSlug,
		At:           job.EventAt,
		DashboardURL: a.dashboardURL(job.MonitorSlug),
	}

	switch {
	case job.IsReminder:
		// A reminder reuses the outage's down event, so it is detected by the
		// IsReminder flag, not the event type. "Still down for X" is measured
		// from that down event up to now.
		n.Kind = alert.KindReminder
		d, err := a.st.ResolveDowntime(ctx, job.MonitorID, time.Now())
		if err != nil {
			return alert.Message{}, fmt.Errorf("resolve downtime: %w", err)
		}
		n.Downtime = d
	case job.EventType == "up":
		// Downtime for the recovery line is measured from the outage's down
		// event up to this recovery event.
		n.Kind = alert.KindUp
		d, err := a.st.ResolveDowntime(ctx, job.MonitorID, job.EventAt)
		if err != nil {
			return alert.Message{}, fmt.Errorf("resolve downtime: %w", err)
		}
		n.Downtime = d
	default: // "down" and any other alerting transition
		// Reason left empty: the template's default "check failed" reads better
		// than the raw event type.
		n.Kind = alert.KindDown
	}

	msg := alert.Render(n)
	msg.To = job.Recipient
	return msg, nil
}

func (a *Alerter) dashboardURL(slug string) string {
	if a.baseURL == "" || slug == "" {
		return ""
	}
	return fmt.Sprintf("%s/monitors/%s", a.baseURL, slug)
}

// backoff returns the delay before the next attempt given how many attempts have
// already happened: 1m, 5m, 25m (baseBackoff * backoffMult^attempts).
func backoff(attempts int) time.Duration {
	d := baseBackoff
	for range attempts {
		d *= backoffMult
	}
	return d
}
