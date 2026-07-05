package store

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sukhera/ping/db"
	"github.com/sukhera/ping/schedule"
)

// Checkin is the store's domain representation of a checkins row (PING-014
// check-in log). SourceIP/UserAgent/Body are pointers: nil means the column
// was NULL (no source IP recorded, no body sent, etc.), distinct from "".
type Checkin struct {
	ID        int64
	MonitorID string
	Kind      string
	SourceIP  *string
	UserAgent *string
	Body      *string
	CreatedAt time.Time
}

// CheckinPage is one page of a cursor-paginated check-in log. NextCursor is
// empty when there are no more pages — mirrors EventPage.
type CheckinPage struct {
	Checkins   []Checkin
	NextCursor string
}

// CheckinKind is the kind of ping received. It matches the checkins.kind CHECK
// constraint ('success' | 'start' | 'fail') in migration 000003.
type CheckinKind string

const (
	CheckinSuccess CheckinKind = "success"
	CheckinStart   CheckinKind = "start"
	CheckinFail    CheckinKind = "fail"
)

// alertChannelDefault is the sentinel channel written on outbox alert rows
// until PING-012 introduces real notification channels and fans a transition
// out across them.
const alertChannelDefault = "default"

// RecordCheckinParams carries one ingested ping. Body is already truncated to
// the 10 KB cap by the server handler; SourceIP/UserAgent are stored as NULL
// when empty. Now is injected so tests can pin the deadline math.
type RecordCheckinParams struct {
	Slug      string
	Kind      CheckinKind
	SourceIP  string
	UserAgent string
	Body      string
	Now       time.Time
}

// RecordCheckinResult reports what the check-in did. Transition is "" for a
// no-op check-in (start, or a repeat of the current state), "up" on recovery,
// or "down" on failure — informational only; the ingest path dispatches nothing.
type RecordCheckinResult struct {
	MonitorID    string
	State        string
	Transition   string
	NextDeadline *time.Time
}

// RecordCheckin applies one ping to its monitor in a single transaction: it
// records the checkin row, transitions state, recomputes the deadline on
// success, and — only on an actual up↔down transition — writes a timeline
// event plus a pending outbox alert row (never dispatching; that is PING-012).
//
// The monitor row is locked FOR UPDATE for the whole transaction so concurrent
// pings on the same slug serialize: the second re-reads the post-transition
// state and correctly emits no duplicate event/alert. An unknown slug returns
// ErrNotFound (bare, not HTTP-wrapped) so the ingest handler can answer 200
// without recording anything, per the anti-enumeration decision in docs/API.md.
func (s *Store) RecordCheckin(ctx context.Context, p RecordCheckinParams) (RecordCheckinResult, error) {
	var result RecordCheckinResult

	err := s.withTx(ctx, func(q *db.Queries) error {
		m, err := q.GetMonitorBySlugForUpdate(ctx, p.Slug)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("store: lookup monitor by slug: %w", err)
		}

		if _, err := q.InsertCheckin(ctx, db.InsertCheckinParams{
			MonitorID: m.ID,
			Kind:      string(p.Kind),
			SourceIp:  parseSourceIP(p.SourceIP),
			UserAgent: textOrNull(p.UserAgent),
			Body:      textOrNull(p.Body),
		}); err != nil {
			return fmt.Errorf("store: insert checkin: %w", err)
		}

		result.MonitorID = m.ID.String()
		result.State = m.State

		switch p.Kind {
		case CheckinStart:
			// A "job started" signal: recorded above, but it does not move
			// state, re-arm the deadline, or resume the monitor.
			return nil
		case CheckinSuccess:
			return applySuccess(ctx, q, m, p.Now, &result)
		case CheckinFail:
			return applyFail(ctx, q, m, p.Now, &result)
		default:
			return fmt.Errorf("store: unknown checkin kind %q", p.Kind)
		}
	})
	if err != nil {
		return RecordCheckinResult{}, err
	}
	return result, nil
}

// applySuccess transitions the monitor to up, re-arming next_deadline from now.
// If it was down, this is a recovery: an "up" event and a pending alert row are
// written.
func applySuccess(ctx context.Context, q *db.Queries, m db.Monitor, now time.Time, result *RecordCheckinResult) error {
	nextDeadline, err := nextDeadlineFor(m, now)
	if err != nil {
		return err
	}

	if err := q.UpdateMonitorOnCheckin(ctx, db.UpdateMonitorOnCheckinParams{
		ID:            m.ID,
		State:         "up",
		LastCheckinAt: pgtype.Timestamptz{Time: now, Valid: true},
		NextDeadline:  nextDeadline,
		FailStreak:    0,
	}); err != nil {
		return fmt.Errorf("store: update monitor on success: %w", err)
	}

	result.State = "up"
	result.NextDeadline = timePtr(nextDeadline)

	if m.State == "down" {
		result.Transition = "up"
		return recordTransition(ctx, q, m.ID, "up", "Monitor recovered")
	}
	return nil
}

// applyFail transitions the monitor to down immediately, incrementing the fail
// streak and clearing the deadline. If it was not already down, a "down" event
// and a pending alert row are written (deduped: a repeat fail emits neither).
func applyFail(ctx context.Context, q *db.Queries, m db.Monitor, now time.Time, result *RecordCheckinResult) error {
	if err := q.UpdateMonitorOnCheckin(ctx, db.UpdateMonitorOnCheckinParams{
		ID:            m.ID,
		State:         "down",
		LastCheckinAt: pgtype.Timestamptz{Time: now, Valid: true},
		NextDeadline:  pgtype.Timestamptz{}, // NULL: a down monitor has no pending deadline
		FailStreak:    m.FailStreak + 1,
	}); err != nil {
		return fmt.Errorf("store: update monitor on fail: %w", err)
	}

	result.State = "down"

	if m.State != "down" {
		result.Transition = "down"
		return recordTransition(ctx, q, m.ID, "down", "Monitor reported failure")
	}
	return nil
}

// recordEvent writes a timeline event with NO outbox alert. Used for
// transitions that should not page anyone — e.g. the intermediate up→late step,
// which is informational only (an alert fires on →down, not on lateness).
func recordEvent(ctx context.Context, q *db.Queries, monitorID pgtype.UUID, eventType, message string) (db.Event, error) {
	return recordEventMeta(ctx, q, monitorID, eventType, message, nil)
}

// recordEventMeta is recordEvent with a JSON meta payload (nil → '{}'). Used
// for events that carry structured detail, e.g. config_change's changed-field
// list (PING-010).
func recordEventMeta(ctx context.Context, q *db.Queries, monitorID pgtype.UUID, eventType, message string, meta []byte) (db.Event, error) {
	event, err := q.InsertEvent(ctx, db.InsertEventParams{
		MonitorID: monitorID,
		Type:      eventType,
		Message:   message,
		Meta:      meta,
	})
	if err != nil {
		return db.Event{}, fmt.Errorf("store: insert event: %w", err)
	}
	return event, nil
}

// recordTransition writes the timeline event AND its pending outbox alert row
// for an alerting transition (up/down). The ingest and scheduler paths never
// dispatch — the alerter worker (PING-012) claims the pending row.
func recordTransition(ctx context.Context, q *db.Queries, monitorID pgtype.UUID, eventType, message string) error {
	event, err := recordEvent(ctx, q, monitorID, eventType, message)
	if err != nil {
		return err
	}

	if _, err := q.InsertAlert(ctx, db.InsertAlertParams{
		MonitorID: monitorID,
		EventID:   event.ID,
		Channel:   alertChannelDefault,
	}); err != nil {
		return fmt.Errorf("store: insert alert: %w", err)
	}
	return nil
}

// nextDeadlineFor computes the re-armed next_deadline for a heartbeat monitor's
// successful check-in at now. This is the bare scheduled OCCURRENCE (the "late"
// threshold), not occurrence + grace: next_deadline always holds the next
// instant the scheduler must act on, and the first such instant after a
// check-in is when the next ping is due (→ late). The scheduler re-arms to
// occurrence + grace when it moves the monitor to late (see store/scheduler.go).
// Non-heartbeat monitors (http) and heartbeats without a schedule get a NULL
// deadline — they are not deadline-driven here.
func nextDeadlineFor(m db.Monitor, now time.Time) (pgtype.Timestamptz, error) {
	if m.Kind != "heartbeat" || !m.ScheduleKind.Valid {
		return pgtype.Timestamptz{}, nil
	}

	cfg := scheduleConfig(m)
	occurrence, err := schedule.NextOccurrence(cfg, now, now)
	if err != nil {
		// A stored schedule that no longer validates is a data bug, not a
		// client error; surface it rather than silently skipping the deadline.
		return pgtype.Timestamptz{}, fmt.Errorf("store: recompute deadline: %w", err)
	}
	return pgtype.Timestamptz{Time: occurrence, Valid: true}, nil
}

// scheduleConfig builds a schedule.Config from a stored monitor row, converting
// the period_s/grace_s second columns to durations. Mirrors the conversion in
// server/monitor.go's mergeScheduleConfig, kept here so the store owns the
// scheduling math (package direction: server → store → db, schedule is pure).
func scheduleConfig(m db.Monitor) schedule.Config {
	cfg := schedule.Config{
		Kind:     schedule.Kind(m.ScheduleKind.String),
		CronExpr: m.CronExpr.String,
		TZ:       m.Tz,
	}
	if m.PeriodS.Valid {
		cfg.Period = time.Duration(m.PeriodS.Int32) * time.Second
	}
	if m.GraceS.Valid {
		cfg.Grace = time.Duration(m.GraceS.Int32) * time.Second
	}
	return cfg
}

// parseSourceIP converts a client IP string to the *netip.Addr sqlc expects for
// the INET column, returning nil (→ NULL) when empty or unparseable.
func parseSourceIP(ip string) *netip.Addr {
	if ip == "" {
		return nil
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return nil
	}
	return &addr
}

// ListCheckinsByMonitor returns one monitor's check-in log, newest first.
// Ownership must already be established by the caller (mirrors
// ListEventsByMonitor's contract).
func (s *Store) ListCheckinsByMonitor(ctx context.Context, monitorID, cursor string, limit int32) (CheckinPage, error) {
	monitorUUID, err := pgUUID(monitorID)
	if err != nil {
		return CheckinPage{}, newHTTPError(ErrNotFound, http.StatusNotFound)
	}

	cursorID, err := decodeIDCursor(cursor)
	if err != nil {
		return CheckinPage{}, newHTTPError(fmt.Errorf("invalid cursor"), http.StatusBadRequest)
	}

	rows, err := s.q.ListCheckinsByMonitorPage(ctx, db.ListCheckinsByMonitorPageParams{
		MonitorID: monitorUUID,
		CursorID:  cursorID,
		PageLimit: limit,
	})
	if err != nil {
		return CheckinPage{}, fmt.Errorf("store: list checkins by monitor: %w", err)
	}
	return toCheckinPage(rows, limit), nil
}

// toCheckinPage converts sqlc rows to a CheckinPage, setting NextCursor when
// the page is full (mirrors toEventPage's convention).
func toCheckinPage(rows []db.Checkin, limit int32) CheckinPage {
	page := CheckinPage{Checkins: make([]Checkin, len(rows))}
	for i, r := range rows {
		page.Checkins[i] = Checkin{
			ID:        r.ID,
			MonitorID: r.MonitorID.String(),
			Kind:      r.Kind,
			SourceIP:  sourceIPString(r.SourceIp),
			UserAgent: textPtr(r.UserAgent),
			Body:      textPtr(r.Body),
			CreatedAt: r.CreatedAt.Time,
		}
	}
	if len(rows) == int(limit) {
		page.NextCursor = encodeIDCursor(rows[len(rows)-1].ID)
	}
	return page
}

// sourceIPString converts a nullable *netip.Addr to *string, nil when absent.
func sourceIPString(addr *netip.Addr) *string {
	if addr == nil {
		return nil
	}
	s := addr.String()
	return &s
}

// textPtr converts a pgtype.Text to *string, nil when NULL.
func textPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	return &t.String
}
