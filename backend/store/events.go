package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sukhera/ping/db"
)

// Event is the store's domain representation of an events row.
type Event struct {
	ID        int64
	MonitorID string
	Type      string
	Message   string
	Meta      []byte
	CreatedAt time.Time
}

// EventPage is one page of a cursor-paginated event feed. NextCursor is empty
// when there are no more pages.
type EventPage struct {
	Events     []Event
	NextCursor string
}

// PauseMonitor sets the paused flag on a monitor owned by callerUserID and
// records a "pause" event, atomically. state is left untouched — paused is a
// flag, not a state (TECH-PLAN §2.3). Returns ErrNotFound for a foreign or
// missing id.
func (s *Store) PauseMonitor(ctx context.Context, id, callerUserID string) (Monitor, error) {
	return s.flagMonitor(ctx, id, callerUserID, func(q *db.Queries, ids monitorIDs) (db.Monitor, error) {
		return q.PauseMonitor(ctx, db.PauseMonitorParams{ID: ids.id, UserID: ids.user})
	}, "pause", "Monitor paused")
}

// MuteMonitor suppresses alerts for a monitor and records a "mute" event.
func (s *Store) MuteMonitor(ctx context.Context, id, callerUserID string) (Monitor, error) {
	return s.flagMonitor(ctx, id, callerUserID, func(q *db.Queries, ids monitorIDs) (db.Monitor, error) {
		return q.MuteMonitor(ctx, db.MuteMonitorParams{ID: ids.id, UserID: ids.user})
	}, "mute", "Alerts muted")
}

// UnmuteMonitor re-enables alerts for a monitor and records an "unmute" event.
func (s *Store) UnmuteMonitor(ctx context.Context, id, callerUserID string) (Monitor, error) {
	return s.flagMonitor(ctx, id, callerUserID, func(q *db.Queries, ids monitorIDs) (db.Monitor, error) {
		return q.UnmuteMonitor(ctx, db.UnmuteMonitorParams{ID: ids.id, UserID: ids.user})
	}, "unmute", "Alerts unmuted")
}

// ResumeMonitor clears the paused flag, sets state back to 'up', and re-arms
// next_deadline from now (so a monitor paused past its deadline does not trip
// late/down the instant it resumes), recording a "resume" event — all
// atomically. now is injected for test determinism. Returns ErrNotFound for a
// foreign or missing id.
func (s *Store) ResumeMonitor(ctx context.Context, id, callerUserID string, now time.Time) (Monitor, error) {
	ids, err := parseMonitorIDs(id, callerUserID)
	if err != nil {
		return Monitor{}, err
	}

	var out Monitor
	err = s.withTx(ctx, func(q *db.Queries) error {
		// Read the (owned) monitor first to build its schedule config for the
		// deadline re-arm. GetMonitorByID doesn't filter by user, so the
		// ownership check is the UPDATE's WHERE below.
		m, err := q.GetMonitorByID(ctx, ids.id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return newHTTPError(ErrNotFound, http.StatusNotFound)
			}
			return fmt.Errorf("store: resume: load monitor: %w", err)
		}

		nextDeadline, err := nextDeadlineFor(m, now)
		if err != nil {
			return err
		}

		row, err := q.ResumeMonitor(ctx, db.ResumeMonitorParams{
			ID:           ids.id,
			UserID:       ids.user,
			NextDeadline: nextDeadline,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return newHTTPError(ErrNotFound, http.StatusNotFound)
			}
			return fmt.Errorf("store: resume monitor: %w", err)
		}
		if _, err := recordEvent(ctx, q, row.ID, "resume", "Monitor resumed"); err != nil {
			return err
		}
		out = toMonitor(row)
		return nil
	})
	if err != nil {
		return Monitor{}, err
	}
	return out, nil
}

// flagMonitor runs a single-column monitor flag update (pause/mute/unmute) plus
// its event in one transaction. update returns the RETURNING row (or
// pgx.ErrNoRows for a foreign/missing id).
func (s *Store) flagMonitor(
	ctx context.Context,
	id, callerUserID string,
	update func(q *db.Queries, ids monitorIDs) (db.Monitor, error),
	eventType, message string,
) (Monitor, error) {
	ids, err := parseMonitorIDs(id, callerUserID)
	if err != nil {
		return Monitor{}, err
	}

	var out Monitor
	err = s.withTx(ctx, func(q *db.Queries) error {
		row, err := update(q, ids)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return newHTTPError(ErrNotFound, http.StatusNotFound)
			}
			return fmt.Errorf("store: flag monitor (%s): %w", eventType, err)
		}
		if _, err := recordEvent(ctx, q, row.ID, eventType, message); err != nil {
			return err
		}
		out = toMonitor(row)
		return nil
	})
	if err != nil {
		return Monitor{}, err
	}
	return out, nil
}

// ListEventsByUser returns the caller's events across all their monitors,
// newest first, optionally filtered by monitorID and/or eventType. Empty
// monitorID/eventType/cursor mean "no filter".
func (s *Store) ListEventsByUser(ctx context.Context, userID, monitorID, eventType, cursor string, limit int32) (EventPage, error) {
	userUUID, err := pgUUID(userID)
	if err != nil {
		return EventPage{}, newHTTPError(ErrNotFound, http.StatusNotFound)
	}

	monitorUUID, err := optionalUUID(monitorID)
	if err != nil {
		// A malformed monitor filter matches nothing rather than erroring.
		return EventPage{}, nil
	}

	cursorID, err := decodeIDCursor(cursor)
	if err != nil {
		return EventPage{}, newHTTPError(fmt.Errorf("invalid cursor"), http.StatusBadRequest)
	}

	rows, err := s.q.ListEventsByUserPage(ctx, db.ListEventsByUserPageParams{
		UserID:    userUUID,
		MonitorID: monitorUUID,
		EventType: textOrNull(eventType),
		CursorID:  cursorID,
		PageLimit: limit,
	})
	if err != nil {
		return EventPage{}, fmt.Errorf("store: list events by user: %w", err)
	}
	return toEventPage(rows, limit), nil
}

// ListEventsByMonitor returns one monitor's events, newest first, optionally
// filtered by eventType. Ownership must already be established by the caller.
func (s *Store) ListEventsByMonitor(ctx context.Context, monitorID, eventType, cursor string, limit int32) (EventPage, error) {
	monitorUUID, err := pgUUID(monitorID)
	if err != nil {
		return EventPage{}, newHTTPError(ErrNotFound, http.StatusNotFound)
	}

	cursorID, err := decodeIDCursor(cursor)
	if err != nil {
		return EventPage{}, newHTTPError(fmt.Errorf("invalid cursor"), http.StatusBadRequest)
	}

	rows, err := s.q.ListEventsByMonitorPage(ctx, db.ListEventsByMonitorPageParams{
		MonitorID: monitorUUID,
		EventType: textOrNull(eventType),
		CursorID:  cursorID,
		PageLimit: limit,
	})
	if err != nil {
		return EventPage{}, fmt.Errorf("store: list events by monitor: %w", err)
	}
	return toEventPage(rows, limit), nil
}

// monitorIDs holds the parsed id + user UUIDs for a monitor mutation.
type monitorIDs struct {
	id   pgtype.UUID
	user pgtype.UUID
}

// parseMonitorIDs parses id + callerUserID, mapping bad UUIDs to ErrNotFound
// (a malformed id can't name an existing owned monitor).
func parseMonitorIDs(id, callerUserID string) (monitorIDs, error) {
	idUUID, err := pgUUID(id)
	if err != nil {
		return monitorIDs{}, newHTTPError(ErrNotFound, http.StatusNotFound)
	}
	userUUID, err := pgUUID(callerUserID)
	if err != nil {
		return monitorIDs{}, newHTTPError(ErrNotFound, http.StatusNotFound)
	}
	return monitorIDs{id: idUUID, user: userUUID}, nil
}

// optionalUUID parses a possibly-empty UUID string. "" → invalid pgtype.UUID
// (the query treats it as "no filter" via IS NULL).
func optionalUUID(s string) (pgtype.UUID, error) {
	if s == "" {
		return pgtype.UUID{}, nil
	}
	return pgUUID(s)
}

func toEventPage(rows []db.Event, limit int32) EventPage {
	page := EventPage{Events: make([]Event, len(rows))}
	for i, r := range rows {
		page.Events[i] = Event{
			ID:        r.ID,
			MonitorID: r.MonitorID.String(),
			Type:      r.Type,
			Message:   r.Message,
			Meta:      r.Meta,
			CreatedAt: r.CreatedAt.Time,
		}
	}
	if len(rows) == int(limit) {
		page.NextCursor = encodeIDCursor(rows[len(rows)-1].ID)
	}
	return page
}

// encodeIDCursor opaque-encodes a BIGSERIAL id for pagination. Shared by
// every feed keyed on a monotonic bigint id (events, checkins) — a single id
// is a strict, stable cursor for all of them.
func encodeIDCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

// decodeIDCursor decodes a cursor from encodeIDCursor. An empty cursor
// yields an invalid pgtype.Int8 (the query treats it as "first page").
func decodeIDCursor(cursor string) (pgtype.Int8, error) {
	if cursor == "" {
		return pgtype.Int8{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return pgtype.Int8{}, err
	}
	id, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return pgtype.Int8{}, err
	}
	return pgtype.Int8{Int64: id, Valid: true}, nil
}
