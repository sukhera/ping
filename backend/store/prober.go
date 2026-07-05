package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sukhera/ping/db"
)

// ClaimDueProbes claims up to limit http monitors whose next_probe_at has
// passed, locking each with FOR UPDATE SKIP LOCKED so a second prober replica
// claims different rows. This is a single statement (no explicit
// multi-statement transaction): pgx wraps it in its own implicit transaction,
// so the row locks are released the instant the statement completes — the
// caller must not hold onto the claim while doing the (slow, external) HTTP
// probe. now is injected for test determinism (pass time.Now() in prod).
func (s *Store) ClaimDueProbes(ctx context.Context, now time.Time, limit int32) ([]Monitor, error) {
	rows, err := s.q.ClaimDueProbes(ctx, db.ClaimDueProbesParams{
		Now:       pgtype.Timestamptz{Time: now, Valid: true},
		PageLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("store: claim due probes: %w", err)
	}
	monitors := make([]Monitor, len(rows))
	for i, row := range rows {
		monitors[i] = toMonitor(row)
	}
	return monitors, nil
}

// ProbeOutcome is what the prober worker observed for one HTTP monitor,
// independent of the monitor's prior state — the store computes the
// confirmation-threshold transition from this plus the monitor's current
// fail_streak/state.
type ProbeOutcome struct {
	MonitorID    string
	OK           bool
	HTTPStatus   *int32
	LatencyMS    *int32
	Error        string // "" on success
	TLSExpiresAt *time.Time
	// FailThreshold is the monitor's configured confirmation threshold
	// (defaults applied by the caller — PRD F2.2 default is 2).
	FailThreshold int32
	// PriorState/PriorFailStreak are the values the claim observed, so the
	// transition logic doesn't need a second read inside the transaction.
	PriorState      string
	PriorFailStreak int32
	// PriorTLSWarnedExpiresAt is the monitor's tls_warned_expires_at column at
	// claim time, so the TLS-expiry warning check doesn't need a second read
	// inside the transaction (same pattern as PriorState/PriorFailStreak).
	PriorTLSWarnedExpiresAt *time.Time
	// NextProbeAt is now + the monitor's interval_s, computed by the caller.
	NextProbeAt time.Time
	// Now is injected for test determinism (pass time.Now() in prod) — it is
	// compared against TLSExpiresAt to decide whether the cert is inside the
	// warning window.
	Now time.Time
}

// tlsWarningWindow is how far ahead of expiry a certificate triggers a
// warning (TECH-PLAN PING-018 AC: "cert expiring in 13 days → exactly one
// warning event+email").
const tlsWarningWindow = 14 * 24 * time.Hour

// RecordProbeResultResult reports what RecordProbeResult did, mirroring
// RecordCheckinResult's shape. Transition is "" for a no-op probe (repeat
// success, or a failure that hasn't yet reached the threshold).
type RecordProbeResultResult struct {
	State      string
	Transition string
}

// RecordProbeResult applies one probe's outcome to its monitor in a single
// transaction: it inserts the probe_results row and updates the monitor's
// fail_streak/state/next_probe_at, writing a timeline event plus a pending
// outbox alert only on an actual up/down transition (never on an
// unconfirmed failure or a repeat success) — the same confirmed-transition
// shape as store/checkin.go's applySuccess/applyFail and
// store/scheduler.go's evaluateOne.
//
// Confirmation logic (PRD F2.2): a failure increments fail_streak but only
// flips state to "down" once fail_streak reaches FailThreshold — a single
// blip is absorbed. Any success resets fail_streak to 0 and flips state to
// "up" immediately (one success confirms recovery).
func (s *Store) RecordProbeResult(ctx context.Context, o ProbeOutcome) (RecordProbeResultResult, error) {
	var result RecordProbeResultResult

	err := s.withTx(ctx, func(q *db.Queries) error {
		monitorID, err := pgUUID(o.MonitorID)
		if err != nil {
			return err
		}

		if _, err := q.InsertProbeResult(ctx, db.InsertProbeResultParams{
			MonitorID:    monitorID,
			Ok:           o.OK,
			HttpStatus:   nullableInt32(o.HTTPStatus),
			LatencyMs:    nullableInt32(o.LatencyMS),
			Error:        textOrNull(o.Error),
			TlsExpiresAt: nullableTimestamptz(o.TLSExpiresAt),
		}); err != nil {
			return fmt.Errorf("store: insert probe result: %w", err)
		}

		newState, newFailStreak := nextProbeState(o)
		nextProbeAt := pgtype.Timestamptz{Time: o.NextProbeAt, Valid: true}

		if err := q.UpdateMonitorOnProbe(ctx, db.UpdateMonitorOnProbeParams{
			State:       newState,
			FailStreak:  newFailStreak,
			NextProbeAt: nextProbeAt,
			ID:          monitorID,
		}); err != nil {
			return fmt.Errorf("store: update monitor on probe: %w", err)
		}

		result.State = newState

		switch {
		case newState == "down" && o.PriorState != "down":
			result.Transition = "down"
			if err := recordTransition(ctx, q, monitorID, "down", probeFailureMessage(o)); err != nil {
				return err
			}
		case newState == "up" && o.PriorState == "down":
			result.Transition = "up"
			if err := recordTransition(ctx, q, monitorID, "up", "Monitor recovered"); err != nil {
				return err
			}
		}

		return maybeWarnTLSExpiry(ctx, q, monitorID, o)
	})
	if err != nil {
		return RecordProbeResultResult{}, err
	}
	return result, nil
}

// nextProbeState applies the confirmation-threshold rule to one outcome,
// returning the monitor's new state and fail_streak.
func nextProbeState(o ProbeOutcome) (state string, failStreak int32) {
	if o.OK {
		return "up", 0
	}
	failStreak = o.PriorFailStreak + 1
	threshold := o.FailThreshold
	if threshold <= 0 {
		threshold = 2 // PRD F2.2 default
	}
	if failStreak >= threshold {
		return "down", failStreak
	}
	// Below threshold: state is unchanged (a monitor that was already "up"
	// stays "up" through an unconfirmed blip; PriorState covers "new" too).
	if o.PriorState == "" {
		return "up", failStreak
	}
	return o.PriorState, failStreak
}

// probeFailureMessage renders the down-transition event message from the
// probe's recorded error, falling back to a generic message if none was set.
func probeFailureMessage(o ProbeOutcome) string {
	if o.Error == "" {
		return "Monitor failed its probe"
	}
	return "Monitor failed its probe: " + o.Error
}

// tlsExpiryMeta is the events.meta JSON payload for a "tls_expiry" event —
// the alerter (PING-018) reads tls_expires_at back out to render the
// KindTLSExpiry template, the same "meta carries structured detail" pattern
// config_change uses for its changed-field list.
type tlsExpiryMeta struct {
	TLSExpiresAt time.Time `json:"tls_expires_at"`
}

// maybeWarnTLSExpiry records a "tls_expiry" event + pending outbox alert when
// o's certificate is within tlsWarningWindow of expiring and no warning has
// yet been sent for this exact certificate. Comparing against
// PriorTLSWarnedExpiresAt (rather than just "was any warning ever sent") is
// what makes the warning re-arm automatically on renewal: a renewed
// certificate has a different (later) TLSExpiresAt, so the equality check
// fails and a fresh warning fires for the new expiry.
func maybeWarnTLSExpiry(ctx context.Context, q *db.Queries, monitorID pgtype.UUID, o ProbeOutcome) error {
	if o.TLSExpiresAt == nil {
		return nil
	}

	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	if o.TLSExpiresAt.Sub(now) > tlsWarningWindow {
		return nil
	}
	if o.PriorTLSWarnedExpiresAt != nil && o.PriorTLSWarnedExpiresAt.Equal(*o.TLSExpiresAt) {
		return nil
	}

	meta, err := json.Marshal(tlsExpiryMeta{TLSExpiresAt: *o.TLSExpiresAt})
	if err != nil {
		return fmt.Errorf("store: marshal tls_expiry meta: %w", err)
	}
	event, err := recordEventMeta(ctx, q, monitorID, "tls_expiry",
		fmt.Sprintf("TLS certificate expires %s", o.TLSExpiresAt.UTC().Format(time.RFC3339)), meta)
	if err != nil {
		return err
	}
	if _, err := q.InsertAlert(ctx, db.InsertAlertParams{
		MonitorID: monitorID,
		EventID:   event.ID,
		Channel:   alertChannelDefault,
	}); err != nil {
		return fmt.Errorf("store: insert tls_expiry alert: %w", err)
	}

	if err := q.SetTLSWarnedExpiry(ctx, db.SetTLSWarnedExpiryParams{
		ID:           monitorID,
		TlsExpiresAt: pgtype.Timestamptz{Time: *o.TLSExpiresAt, Valid: true},
	}); err != nil {
		return fmt.Errorf("store: set tls warned expiry: %w", err)
	}
	return nil
}

// ProbeResult is the store's domain representation of a probe_results row —
// one HTTP monitor probe attempt (PING-018 probe log).
type ProbeResult struct {
	ID           int64
	MonitorID    string
	OK           bool
	HTTPStatus   *int32
	LatencyMS    *int32
	Error        string
	TLSExpiresAt *time.Time
	CreatedAt    time.Time
}

// ProbeResultPage is one page of a cursor-paginated probe log. NextCursor is
// empty when there are no more pages — mirrors EventPage/CheckinPage.
type ProbeResultPage struct {
	Results    []ProbeResult
	NextCursor string
}

// ListProbeResultsByMonitor returns one monitor's probe log, newest first,
// optionally filtered to only successful or only failed probes. outcome is
// "", "success", or "fail" — anything else matches nothing (handler validates
// the query param before calling this). Ownership must already be
// established by the caller (matches ListCheckinsByMonitor/
// ListEventsByMonitor's convention: this method trusts monitorID is owned).
func (s *Store) ListProbeResultsByMonitor(ctx context.Context, monitorID, outcome, cursor string, limit int32) (ProbeResultPage, error) {
	monitorUUID, err := pgUUID(monitorID)
	if err != nil {
		return ProbeResultPage{}, newHTTPError(ErrNotFound, http.StatusNotFound)
	}

	cursorID, err := decodeIDCursor(cursor)
	if err != nil {
		return ProbeResultPage{}, newHTTPError(fmt.Errorf("invalid cursor"), http.StatusBadRequest)
	}

	rows, err := s.q.ListProbeResultsByMonitor(ctx, db.ListProbeResultsByMonitorParams{
		MonitorID: monitorUUID,
		Ok:        outcomeFilter(outcome),
		CursorID:  cursorID,
		PageLimit: limit,
	})
	if err != nil {
		return ProbeResultPage{}, fmt.Errorf("store: list probe results by monitor: %w", err)
	}
	return toProbeResultPage(rows, limit), nil
}

// outcomeFilter maps the ?outcome query param to the query's nullable
// boolean: "" (no filter) → invalid/NULL, "success" → true, "fail" → false.
func outcomeFilter(outcome string) pgtype.Bool {
	switch outcome {
	case "success":
		return pgtype.Bool{Bool: true, Valid: true}
	case "fail":
		return pgtype.Bool{Bool: false, Valid: true}
	default:
		return pgtype.Bool{}
	}
}

func toProbeResultPage(rows []db.ProbeResult, limit int32) ProbeResultPage {
	page := ProbeResultPage{Results: make([]ProbeResult, len(rows))}
	for i, r := range rows {
		page.Results[i] = ProbeResult{
			ID:           r.ID,
			MonitorID:    r.MonitorID.String(),
			OK:           r.Ok,
			HTTPStatus:   int32Ptr(r.HttpStatus),
			LatencyMS:    int32Ptr(r.LatencyMs),
			Error:        r.Error.String,
			TLSExpiresAt: timePtr(r.TlsExpiresAt),
			CreatedAt:    r.CreatedAt.Time,
		}
	}
	if len(rows) == int(limit) {
		page.NextCursor = encodeIDCursor(rows[len(rows)-1].ID)
	}
	return page
}

// LatencyBucket is one time-bucketed point in a monitor's latency series
// (PING-018 latency chart): p50/p95/avg latency (ms) over successful probes
// in [BucketStart, BucketStart+bucketSeconds). The query's GROUP BY only ever
// produces buckets with at least one successful probe, so P50/P95/Avg are
// always defined (no NULL-bucket case to represent).
type LatencyBucket struct {
	BucketStart time.Time
	P50         float64
	P95         float64
	Avg         float64
	SampleCount int64
}

// LatencySeriesByMonitor returns one monitor's latency series bucketed at
// bucketSeconds over [since, now), for the 24h/7d/30d chart windows — the
// caller (server layer) chooses bucketSeconds per window (e.g. 5m buckets for
// 24h, 1h for 7d, 6h for 30d) so the point count stays chart-sized regardless
// of window length. Ownership must already be established by the caller.
func (s *Store) LatencySeriesByMonitor(ctx context.Context, monitorID string, since time.Time, bucketSeconds int32) ([]LatencyBucket, error) {
	monitorUUID, err := pgUUID(monitorID)
	if err != nil {
		return nil, newHTTPError(ErrNotFound, http.StatusNotFound)
	}

	rows, err := s.q.LatencySeriesByMonitor(ctx, db.LatencySeriesByMonitorParams{
		MonitorID:     monitorUUID,
		Since:         pgtype.Timestamptz{Time: since, Valid: true},
		BucketSeconds: int64(bucketSeconds),
	})
	if err != nil {
		return nil, fmt.Errorf("store: latency series by monitor: %w", err)
	}

	buckets := make([]LatencyBucket, len(rows))
	for i, r := range rows {
		bucketStart, _ := r.BucketStart.(time.Time)
		buckets[i] = LatencyBucket{
			BucketStart: bucketStart,
			P50:         r.P50,
			P95:         r.P95,
			Avg:         r.Avg,
			SampleCount: r.SampleCount,
		}
	}
	return buckets, nil
}

func nullableInt32(v *int32) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *v, Valid: true}
}

func nullableTimestamptz(v *time.Time) pgtype.Timestamptz {
	if v == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *v, Valid: true}
}
