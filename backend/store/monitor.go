package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sukhera/ping/db"
)

// maxSlugRetries bounds slug-collision retries on create. Collision odds at
// 16 random base32 characters (80 bits of entropy) are astronomically low;
// this is defense-in-depth, not an expected code path.
const maxSlugRetries = 5

// Monitor is the store's domain representation of a monitors row: pgtype
// columns are converted to plain Go types at this boundary, matching the
// AuthUser/RefreshToken convention elsewhere in this package. DisplayState
// is derived, not a DB column — "paused" when PausedAt is set, else State
// (TECH-PLAN §2.3: paused is a flag, not a state).
type Monitor struct {
	ID            string
	UserID        string
	Kind          string
	Slug          string
	Name          string
	ScheduleKind  string
	PeriodS       *int32
	CronExpr      string
	TZ            string
	GraceS        *int32
	URL           string
	Method        string
	IntervalS     *int32
	TimeoutS      *int32
	FailThreshold *int32
	HTTPConfig    []byte
	State         string
	DisplayState  string
	FailStreak    int32
	LastCheckinAt *time.Time
	NextDeadline  *time.Time
	NextProbeAt   *time.Time
	AlertsMuted   bool
	PausedAt      *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// MonitorPage is one page of a cursor-paginated monitor list.
// NextCursor is empty when there are no more pages.
type MonitorPage struct {
	Monitors   []Monitor
	NextCursor string
}

// CreateMonitorParams carries the fields callers can set on create. Runtime
// fields (state, fail_streak, timestamps) are DB-owned and not settable here.
type CreateMonitorParams struct {
	UserID string
	Kind   string
	Name   string

	// heartbeat
	ScheduleKind string
	PeriodS      *int32
	CronExpr     string
	TZ           string
	GraceS       *int32

	// http
	URL           string
	Method        string
	IntervalS     *int32
	TimeoutS      *int32
	FailThreshold *int32
	HTTPConfig    []byte
}

// UpdateMonitorParams carries the fields callers may change. A nil pointer
// (or empty string for text fields where empty is never a valid value) means
// "leave unchanged" — see UpdateMonitor's COALESCE-based query.
type UpdateMonitorParams struct {
	Name string // empty = unchanged

	ScheduleKind string
	PeriodS      *int32
	CronExpr     string
	TZ           string
	GraceS       *int32

	URL           string
	Method        string
	IntervalS     *int32
	TimeoutS      *int32
	FailThreshold *int32
	HTTPConfig    []byte
}

// CreateMonitor generates a unique slug and inserts a new monitor owned by
// userID. Slug collisions (vanishingly unlikely at 16 random chars) are
// retried transparently up to maxSlugRetries times.
func (s *Store) CreateMonitor(ctx context.Context, p CreateMonitorParams) (Monitor, error) {
	userUUID, err := pgUUID(p.UserID)
	if err != nil {
		return Monitor{}, err
	}

	params := db.CreateMonitorParams{
		UserID:        userUUID,
		Kind:          p.Kind,
		Name:          p.Name,
		ScheduleKind:  textOrNull(p.ScheduleKind),
		PeriodS:       int4OrNull(p.PeriodS),
		CronExpr:      textOrNull(p.CronExpr),
		Tz:            p.TZ,
		GraceS:        int4OrNull(p.GraceS),
		Url:           textOrNull(p.URL),
		Method:        textOrNull(p.Method),
		IntervalS:     int4OrNull(p.IntervalS),
		TimeoutS:      int4OrNull(p.TimeoutS),
		FailThreshold: int4OrNull(p.FailThreshold),
		HttpConfig:    p.HTTPConfig,
	}

	var row db.Monitor
	for range maxSlugRetries {
		slug, err := generateSlug()
		if err != nil {
			return Monitor{}, err
		}
		params.Slug = slug

		row, err = s.q.CreateMonitor(ctx, params)
		if err == nil {
			return toMonitor(row), nil
		}
		if !isUniqueViolation(err) {
			return Monitor{}, fmt.Errorf("store: create monitor: %w", err)
		}
		// Unique violation: retry with a fresh slug. Postgres unique_violation
		// doesn't distinguish which constraint fired, but slug is the only
		// UNIQUE column this insert touches.
	}
	return Monitor{}, fmt.Errorf("store: create monitor: exhausted %d slug retries", maxSlugRetries)
}

// GetMonitor fetches a monitor by id and verifies callerUserID owns it,
// returning ErrForbidden (not ErrNotFound) on a foreign monitor per the
// security checklist's IDOR guidance — existence isn't hidden from an
// authenticated caller, only cross-account access is denied.
func (s *Store) GetMonitor(ctx context.Context, id, callerUserID string) (Monitor, error) {
	idUUID, err := pgUUID(id)
	if err != nil {
		return Monitor{}, newHTTPError(ErrNotFound, http.StatusNotFound)
	}

	row, err := s.q.GetMonitorByID(ctx, idUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Monitor{}, newHTTPError(ErrNotFound, http.StatusNotFound)
		}
		return Monitor{}, fmt.Errorf("store: get monitor: %w", err)
	}

	if row.UserID.String() != callerUserID {
		return Monitor{}, newHTTPError(ErrForbidden, http.StatusForbidden)
	}

	return toMonitor(row), nil
}

// ListMonitors returns one cursor-paginated page of userID's monitors,
// ordered newest first. cursor is the opaque string from a previous page's
// MonitorPage.NextCursor, or empty for the first page.
func (s *Store) ListMonitors(ctx context.Context, userID, cursor string, limit int32) (MonitorPage, error) {
	userUUID, err := pgUUID(userID)
	if err != nil {
		return MonitorPage{}, err
	}

	params := db.ListMonitorsByUserPageParams{
		UserID:    userUUID,
		PageLimit: limit,
	}
	if cursor != "" {
		createdAt, id, err := decodeCursor(cursor)
		if err != nil {
			return MonitorPage{}, newHTTPError(fmt.Errorf("invalid cursor"), http.StatusBadRequest)
		}
		params.CursorCreatedAt = pgtype.Timestamptz{Time: createdAt, Valid: true}
		params.CursorID = id
	}

	rows, err := s.q.ListMonitorsByUserPage(ctx, params)
	if err != nil {
		return MonitorPage{}, fmt.Errorf("store: list monitors: %w", err)
	}

	page := MonitorPage{Monitors: make([]Monitor, len(rows))}
	for i, row := range rows {
		page.Monitors[i] = toMonitor(row)
	}
	if len(rows) == int(limit) {
		last := rows[len(rows)-1]
		page.NextCursor = encodeCursor(last.CreatedAt.Time, last.ID)
	}
	return page, nil
}

// UpdateMonitor applies a partial update to a monitor owned by callerUserID.
// Fields left zero-valued in p are unchanged (see UpdateMonitorParams).
// Returns ErrNotFound if id doesn't exist or isn't owned by callerUserID —
// the id+user_id WHERE can't distinguish "missing" from "foreign", so unlike
// GetMonitor this can't separate 404 from 403. Callers that need the 403
// distinction (any handler reachable after an existence check) should call
// GetMonitor first to establish ownership, then Update.
func (s *Store) UpdateMonitor(ctx context.Context, id, callerUserID string, p UpdateMonitorParams) (Monitor, error) {
	idUUID, err := pgUUID(id)
	if err != nil {
		return Monitor{}, newHTTPError(ErrNotFound, http.StatusNotFound)
	}
	userUUID, err := pgUUID(callerUserID)
	if err != nil {
		return Monitor{}, newHTTPError(ErrNotFound, http.StatusNotFound)
	}

	row, err := s.q.UpdateMonitor(ctx, db.UpdateMonitorParams{
		ID:            idUUID,
		UserID:        userUUID,
		Name:          textOrNull(p.Name),
		ScheduleKind:  textOrNull(p.ScheduleKind),
		PeriodS:       int4OrNull(p.PeriodS),
		CronExpr:      textOrNull(p.CronExpr),
		Tz:            textOrNull(p.TZ),
		GraceS:        int4OrNull(p.GraceS),
		Url:           textOrNull(p.URL),
		Method:        textOrNull(p.Method),
		IntervalS:     int4OrNull(p.IntervalS),
		TimeoutS:      int4OrNull(p.TimeoutS),
		FailThreshold: int4OrNull(p.FailThreshold),
		HttpConfig:    p.HTTPConfig,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Monitor{}, newHTTPError(ErrNotFound, http.StatusNotFound)
		}
		return Monitor{}, fmt.Errorf("store: update monitor: %w", err)
	}

	return toMonitor(row), nil
}

// DeleteMonitor removes a monitor owned by callerUserID. Idempotent-ish: a
// foreign or already-deleted id both report ErrNotFound (the DELETE...WHERE
// can't distinguish the two, same limitation as UpdateMonitor).
func (s *Store) DeleteMonitor(ctx context.Context, id, callerUserID string) error {
	idUUID, err := pgUUID(id)
	if err != nil {
		return newHTTPError(ErrNotFound, http.StatusNotFound)
	}
	userUUID, err := pgUUID(callerUserID)
	if err != nil {
		return newHTTPError(ErrNotFound, http.StatusNotFound)
	}

	rowsAffected, err := s.q.DeleteMonitor(ctx, db.DeleteMonitorParams{ID: idUUID, UserID: userUUID})
	if err != nil {
		return fmt.Errorf("store: delete monitor: %w", err)
	}
	if rowsAffected == 0 {
		return newHTTPError(ErrNotFound, http.StatusNotFound)
	}
	return nil
}

func toMonitor(row db.Monitor) Monitor {
	m := Monitor{
		ID:           row.ID.String(),
		UserID:       row.UserID.String(),
		Kind:         row.Kind,
		Slug:         row.Slug,
		Name:         row.Name,
		ScheduleKind: row.ScheduleKind.String,
		CronExpr:     row.CronExpr.String,
		TZ:           row.Tz,
		URL:          row.Url.String,
		Method:       row.Method.String,
		HTTPConfig:   row.HttpConfig,
		State:        row.State,
		FailStreak:   row.FailStreak,
		AlertsMuted:  row.AlertsMuted,
		CreatedAt:    row.CreatedAt.Time,
		UpdatedAt:    row.UpdatedAt.Time,
	}

	m.PeriodS = int32Ptr(row.PeriodS)
	m.GraceS = int32Ptr(row.GraceS)
	m.IntervalS = int32Ptr(row.IntervalS)
	m.TimeoutS = int32Ptr(row.TimeoutS)
	m.FailThreshold = int32Ptr(row.FailThreshold)
	m.LastCheckinAt = timePtr(row.LastCheckinAt)
	m.NextDeadline = timePtr(row.NextDeadline)
	m.NextProbeAt = timePtr(row.NextProbeAt)
	m.PausedAt = timePtr(row.PausedAt)

	if row.PausedAt.Valid {
		m.DisplayState = "paused"
	} else {
		m.DisplayState = row.State
	}

	return m
}

func textOrNull(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func int4OrNull(v *int32) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *v, Valid: true}
}

func int32Ptr(v pgtype.Int4) *int32 {
	if !v.Valid {
		return nil
	}
	return &v.Int32
}

func timePtr(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	return &v.Time
}

// encodeCursor/decodeCursor make the (created_at, id) composite pagination
// key an opaque string, so API consumers don't need to know the list is
// paginated on two columns.
func encodeCursor(createdAt time.Time, id pgtype.UUID) string {
	raw := fmt.Sprintf("%s|%s", createdAt.UTC().Format(time.RFC3339Nano), id.String())
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor string) (time.Time, pgtype.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, pgtype.UUID{}, fmt.Errorf("decode cursor: %w", err)
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, pgtype.UUID{}, fmt.Errorf("malformed cursor")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, pgtype.UUID{}, fmt.Errorf("parse cursor timestamp: %w", err)
	}
	id, err := pgUUID(parts[1])
	if err != nil {
		return time.Time{}, pgtype.UUID{}, fmt.Errorf("parse cursor id: %w", err)
	}
	return createdAt, id, nil
}
