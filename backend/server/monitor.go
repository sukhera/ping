package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sukhera/ping/schedule"
	"github.com/sukhera/ping/store"
)

const (
	// maxMonitorBodyBytes bounds request bodies for monitor create/update and
	// the schedule describe preview — generous enough for http_config headers
	// but not unbounded.
	maxMonitorBodyBytes = 16 << 10 // 16 KiB

	defaultPageLimit = 20
	maxPageLimit     = 100

	// PRD F2.1 http monitor bounds.
	minIntervalS = 30
	maxIntervalS = 24 * 60 * 60
	minTimeoutS  = 1
	maxTimeoutS  = 30
)

// monitorStore is the subset of *store.Store the monitor handlers need, kept
// as an interface so handler tests can inject a fake without touching
// Postgres, matching authStore in auth.go.
type monitorStore interface {
	CreateMonitor(ctx context.Context, p store.CreateMonitorParams) (store.Monitor, error)
	GetMonitor(ctx context.Context, id, callerUserID string) (store.Monitor, error)
	ListMonitors(ctx context.Context, userID, cursor string, limit int32) (store.MonitorPage, error)
	UpdateMonitor(ctx context.Context, id, callerUserID string, p store.UpdateMonitorParams) (store.Monitor, error)
	DeleteMonitor(ctx context.Context, id, callerUserID string) error

	PauseMonitor(ctx context.Context, id, callerUserID string) (store.Monitor, error)
	ResumeMonitor(ctx context.Context, id, callerUserID string, now time.Time) (store.Monitor, error)
	MuteMonitor(ctx context.Context, id, callerUserID string) (store.Monitor, error)
	UnmuteMonitor(ctx context.Context, id, callerUserID string) (store.Monitor, error)

	ListEventsByUser(ctx context.Context, userID, monitorID, eventType, cursor string, limit int32) (store.EventPage, error)
	ListEventsByMonitor(ctx context.Context, monitorID, eventType, cursor string, limit int32) (store.EventPage, error)
}

type monitorHandler struct {
	store monitorStore
	deps  Deps
}

func newMonitorHandler(s monitorStore, deps Deps) *monitorHandler {
	return &monitorHandler{store: s, deps: deps}
}

// fieldErrorResponse is a 422 response naming the offending field, used for
// both schedule.ValidationError and the handler's own kind-conditional
// checks — one shape for every field-level validation failure.
type fieldErrorResponse struct {
	Error string `json:"error"`
	Field string `json:"field"`
}

func writeFieldError(w http.ResponseWriter, field, message string) {
	writeJSON(w, http.StatusUnprocessableEntity, fieldErrorResponse{Error: message, Field: field})
}

// scheduleFields mirrors the schedule/http portions of a monitor request
// body. Present on create/update requests and the describe-preview request.
type scheduleFields struct {
	ScheduleKind string `json:"schedule_kind,omitempty"`
	PeriodS      *int32 `json:"period_s,omitempty"`
	CronExpr     string `json:"cron_expr,omitempty"`
	TZ           string `json:"tz,omitempty"`
	GraceS       *int32 `json:"grace_s,omitempty"`
}

type httpFields struct {
	URL           string          `json:"url,omitempty"`
	Method        string          `json:"method,omitempty"`
	IntervalS     *int32          `json:"interval_s,omitempty"`
	TimeoutS      *int32          `json:"timeout_s,omitempty"`
	FailThreshold *int32          `json:"fail_threshold,omitempty"`
	HTTPConfig    json.RawMessage `json:"http_config,omitempty"`
}

type createMonitorRequest struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	AutoResume *bool  `json:"auto_resume,omitempty"`
	scheduleFields
	httpFields
}

type updateMonitorRequest struct {
	Name       *string `json:"name,omitempty"`
	AutoResume *bool   `json:"auto_resume,omitempty"`
	scheduleFields
	httpFields
}

type monitorResponse struct {
	ID            string     `json:"id"`
	Kind          string     `json:"kind"`
	Slug          string     `json:"slug"`
	Name          string     `json:"name"`
	State         string     `json:"state"`
	DisplayState  string     `json:"display_state"`
	PingURL       string     `json:"ping_url,omitempty"`
	ScheduleKind  string     `json:"schedule_kind,omitempty"`
	PeriodS       *int32     `json:"period_s,omitempty"`
	CronExpr      string     `json:"cron_expr,omitempty"`
	TZ            string     `json:"tz,omitempty"`
	GraceS        *int32     `json:"grace_s,omitempty"`
	URL           string     `json:"url,omitempty"`
	Method        string     `json:"method,omitempty"`
	IntervalS     *int32     `json:"interval_s,omitempty"`
	TimeoutS      *int32     `json:"timeout_s,omitempty"`
	FailThreshold *int32     `json:"fail_threshold,omitempty"`
	FailStreak    int32      `json:"fail_streak"`
	AlertsMuted   bool       `json:"alerts_muted"`
	AutoResume    bool       `json:"auto_resume"`
	LastCheckinAt *time.Time `json:"last_checkin_at,omitempty"`
	NextDeadline  *time.Time `json:"next_deadline,omitempty"`
	PausedAt      *time.Time `json:"paused_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func toMonitorResponse(m store.Monitor, baseURL string) monitorResponse {
	resp := monitorResponse{
		ID:            m.ID,
		Kind:          m.Kind,
		Slug:          m.Slug,
		Name:          m.Name,
		State:         m.State,
		DisplayState:  m.DisplayState,
		ScheduleKind:  m.ScheduleKind,
		PeriodS:       m.PeriodS,
		CronExpr:      m.CronExpr,
		TZ:            m.TZ,
		GraceS:        m.GraceS,
		URL:           m.URL,
		Method:        m.Method,
		IntervalS:     m.IntervalS,
		TimeoutS:      m.TimeoutS,
		FailThreshold: m.FailThreshold,
		FailStreak:    m.FailStreak,
		AlertsMuted:   m.AlertsMuted,
		AutoResume:    m.AutoResume,
		LastCheckinAt: m.LastCheckinAt,
		NextDeadline:  m.NextDeadline,
		PausedAt:      m.PausedAt,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
	if m.Kind == "heartbeat" {
		resp.PingURL = baseURL + "/p/" + m.Slug
	}
	return resp
}

func (h *monitorHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createMonitorRequest
	if !decodeBoundedJSON(w, r, &req, maxMonitorBodyBytes) {
		return
	}

	params, ok := h.validateCreate(w, req)
	if !ok {
		return
	}
	params.UserID = userIDFromContext(r.Context())

	m, err := h.store.CreateMonitor(r.Context(), params)
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, toMonitorResponse(m, h.deps.BaseURL))
}

func (h *monitorHandler) get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := h.store.GetMonitor(r.Context(), id, userIDFromContext(r.Context()))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toMonitorResponse(m, h.deps.BaseURL))
}

type monitorListResponse struct {
	Monitors   []monitorResponse `json:"monitors"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

func (h *monitorHandler) list(w http.ResponseWriter, r *http.Request) {
	limit := int32(defaultPageLimit)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := parseBoundedInt(raw, 1, maxPageLimit)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
			return
		}
		limit = n
	}
	cursor := r.URL.Query().Get("cursor")

	page, err := h.store.ListMonitors(r.Context(), userIDFromContext(r.Context()), cursor, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := monitorListResponse{Monitors: make([]monitorResponse, len(page.Monitors)), NextCursor: page.NextCursor}
	for i, m := range page.Monitors {
		resp.Monitors[i] = toMonitorResponse(m, h.deps.BaseURL)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *monitorHandler) update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID := userIDFromContext(r.Context())

	// Establish ownership first (403 vs 404) since UpdateMonitor's id+user_id
	// WHERE can't distinguish "missing" from "foreign" — see store/monitor.go.
	existing, err := h.store.GetMonitor(r.Context(), id, userID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	var req updateMonitorRequest
	if !decodeBoundedJSON(w, r, &req, maxMonitorBodyBytes) {
		return
	}

	params, ok := h.validateUpdate(w, existing, req)
	if !ok {
		return
	}

	m, err := h.store.UpdateMonitor(r.Context(), id, userID, params)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toMonitorResponse(m, h.deps.BaseURL))
}

func (h *monitorHandler) delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID := userIDFromContext(r.Context())

	// Establish ownership first for the same 403-vs-404 reason as update.
	if _, err := h.store.GetMonitor(r.Context(), id, userID); err != nil {
		writeError(w, r, err)
		return
	}

	if err := h.store.DeleteMonitor(r.Context(), id, userID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

type describeRequest struct {
	scheduleFields
}

type describeResponse struct {
	Description string `json:"description"`
}

func (h *monitorHandler) describeSchedule(w http.ResponseWriter, r *http.Request) {
	var req describeRequest
	if !decodeBoundedJSON(w, r, &req, maxMonitorBodyBytes) {
		return
	}

	cfg := scheduleConfigFromFields(req.scheduleFields)

	description, err := schedule.Describe(cfg)
	if err != nil {
		writeScheduleValidationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, describeResponse{Description: description})
}

// validateCreate checks kind-conditional required fields (request-shape
// validation, handled in the handler layer per server/auth.go's precedent —
// see PING-007 plan's "Design decisions") and, for heartbeat monitors, runs
// schedule.Config.Validate() for field-level 422 detail.
func (h *monitorHandler) validateCreate(w http.ResponseWriter, req createMonitorRequest) (store.CreateMonitorParams, bool) {
	if req.Name == "" {
		writeFieldError(w, "name", "name is required")
		return store.CreateMonitorParams{}, false
	}

	switch req.Kind {
	case "heartbeat":
		cfg := scheduleConfigFromFields(req.scheduleFields)
		if err := cfg.Validate(); err != nil {
			writeScheduleValidationError(w, err)
			return store.CreateMonitorParams{}, false
		}
		if req.URL != "" || req.Method != "" {
			writeFieldError(w, "url", "must be empty for a heartbeat monitor")
			return store.CreateMonitorParams{}, false
		}
		return store.CreateMonitorParams{
			Kind:         req.Kind,
			Name:         req.Name,
			ScheduleKind: req.ScheduleKind,
			PeriodS:      req.PeriodS,
			CronExpr:     req.CronExpr,
			TZ:           req.TZ,
			GraceS:       req.GraceS,
			AutoResume:   req.AutoResume,
		}, true

	case "http":
		if !h.validateHTTPFields(w, req.httpFields) {
			return store.CreateMonitorParams{}, false
		}
		if req.ScheduleKind != "" || req.CronExpr != "" {
			writeFieldError(w, "schedule_kind", "must be empty for an http monitor")
			return store.CreateMonitorParams{}, false
		}
		return store.CreateMonitorParams{
			Kind:          req.Kind,
			Name:          req.Name,
			URL:           req.URL,
			Method:        req.Method,
			IntervalS:     req.IntervalS,
			TimeoutS:      req.TimeoutS,
			FailThreshold: req.FailThreshold,
			HTTPConfig:    []byte(req.HTTPConfig),
			AutoResume:    req.AutoResume,
		}, true

	default:
		writeFieldError(w, "kind", `must be "heartbeat" or "http"`)
		return store.CreateMonitorParams{}, false
	}
}

// validateUpdate applies the same kind-conditional rules as validateCreate,
// but only to fields present in the partial-update request; existing pins
// down which kind's rules apply since kind itself isn't editable.
func (h *monitorHandler) validateUpdate(w http.ResponseWriter, existing store.Monitor, req updateMonitorRequest) (store.UpdateMonitorParams, bool) {
	params := store.UpdateMonitorParams{}
	if req.Name != nil {
		if *req.Name == "" {
			writeFieldError(w, "name", "name must not be empty")
			return store.UpdateMonitorParams{}, false
		}
		params.Name = *req.Name
	}

	scheduleFieldsSet := req.ScheduleKind != "" || req.PeriodS != nil || req.CronExpr != "" || req.TZ != "" || req.GraceS != nil
	httpFieldsSet := req.URL != "" || req.Method != "" || req.IntervalS != nil || req.TimeoutS != nil || req.FailThreshold != nil || len(req.HTTPConfig) > 0

	switch existing.Kind {
	case "heartbeat":
		if httpFieldsSet {
			writeFieldError(w, "url", "must not be set on a heartbeat monitor")
			return store.UpdateMonitorParams{}, false
		}
		if scheduleFieldsSet {
			cfg := mergeScheduleConfig(existing, req.scheduleFields)
			if err := cfg.Validate(); err != nil {
				writeScheduleValidationError(w, err)
				return store.UpdateMonitorParams{}, false
			}
			params.ScheduleKind = req.ScheduleKind
			params.PeriodS = req.PeriodS
			params.CronExpr = req.CronExpr
			params.TZ = req.TZ
			params.GraceS = req.GraceS
		}

	case "http":
		if scheduleFieldsSet {
			writeFieldError(w, "schedule_kind", "must not be set on an http monitor")
			return store.UpdateMonitorParams{}, false
		}
		if httpFieldsSet {
			merged := mergeHTTPFields(existing, req.httpFields)
			if !h.validateHTTPFields(w, merged) {
				return store.UpdateMonitorParams{}, false
			}
			params.URL = req.URL
			params.Method = req.Method
			params.IntervalS = req.IntervalS
			params.TimeoutS = req.TimeoutS
			params.FailThreshold = req.FailThreshold
			if len(req.HTTPConfig) > 0 {
				params.HTTPConfig = []byte(req.HTTPConfig)
			}
		}
	}

	// auto_resume applies to both kinds and is independent of the schedule/http
	// field groups.
	params.AutoResume = req.AutoResume

	return params, true
}

func (h *monitorHandler) validateHTTPFields(w http.ResponseWriter, f httpFields) bool {
	if f.URL == "" {
		writeFieldError(w, "url", "url is required")
		return false
	}
	if u, err := url.Parse(f.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		writeFieldError(w, "url", "must be a valid http or https URL")
		return false
	}
	if f.Method != "" && f.Method != "GET" && f.Method != "HEAD" {
		writeFieldError(w, "method", `must be "GET" or "HEAD"`)
		return false
	}
	if f.IntervalS != nil && (*f.IntervalS < minIntervalS || *f.IntervalS > maxIntervalS) {
		writeFieldError(w, "interval_s", "must be between 30 and 86400 seconds")
		return false
	}
	if f.TimeoutS != nil && (*f.TimeoutS < minTimeoutS || *f.TimeoutS > maxTimeoutS) {
		writeFieldError(w, "timeout_s", "must be between 1 and 30 seconds")
		return false
	}
	if len(f.HTTPConfig) > 0 && !json.Valid(f.HTTPConfig) {
		writeFieldError(w, "http_config", "must be valid JSON")
		return false
	}
	return true
}

func mergeScheduleConfig(existing store.Monitor, f scheduleFields) schedule.Config {
	cfg := schedule.Config{
		Kind: schedule.Kind(existing.ScheduleKind),
		TZ:   existing.TZ,
	}
	if existing.PeriodS != nil {
		cfg.Period = time.Duration(*existing.PeriodS) * time.Second
	}
	cfg.CronExpr = existing.CronExpr
	if existing.GraceS != nil {
		cfg.Grace = time.Duration(*existing.GraceS) * time.Second
	}

	if f.ScheduleKind != "" {
		cfg.Kind = schedule.Kind(f.ScheduleKind)
	}
	if f.PeriodS != nil {
		cfg.Period = time.Duration(*f.PeriodS) * time.Second
		cfg.CronExpr = ""
	}
	if f.CronExpr != "" {
		cfg.CronExpr = f.CronExpr
		cfg.Period = 0
	}
	if f.TZ != "" {
		cfg.TZ = f.TZ
	}
	if f.GraceS != nil {
		cfg.Grace = time.Duration(*f.GraceS) * time.Second
	}
	return cfg
}

func mergeHTTPFields(existing store.Monitor, f httpFields) httpFields {
	merged := httpFields{
		URL:           existing.URL,
		Method:        existing.Method,
		IntervalS:     existing.IntervalS,
		TimeoutS:      existing.TimeoutS,
		FailThreshold: existing.FailThreshold,
		HTTPConfig:    existing.HTTPConfig,
	}
	if f.URL != "" {
		merged.URL = f.URL
	}
	if f.Method != "" {
		merged.Method = f.Method
	}
	if f.IntervalS != nil {
		merged.IntervalS = f.IntervalS
	}
	if f.TimeoutS != nil {
		merged.TimeoutS = f.TimeoutS
	}
	if f.FailThreshold != nil {
		merged.FailThreshold = f.FailThreshold
	}
	if len(f.HTTPConfig) > 0 {
		merged.HTTPConfig = f.HTTPConfig
	}
	return merged
}

func scheduleConfigFromFields(f scheduleFields) schedule.Config {
	cfg := schedule.Config{
		Kind:     schedule.Kind(f.ScheduleKind),
		CronExpr: f.CronExpr,
		TZ:       f.TZ,
	}
	if f.PeriodS != nil {
		cfg.Period = time.Duration(*f.PeriodS) * time.Second
	}
	if f.GraceS != nil {
		cfg.Grace = time.Duration(*f.GraceS) * time.Second
	}
	return cfg
}

func writeScheduleValidationError(w http.ResponseWriter, err error) {
	if ve, ok := errors.AsType[*schedule.ValidationError](err); ok {
		writeFieldError(w, ve.Field, ve.Message)
		return
	}
	writeJSON(w, http.StatusUnprocessableEntity, errorResponse{Error: err.Error()})
}

// decodeBoundedJSON is decodeJSON (auth.go) generalized over a caller-chosen
// body size limit, since monitor payloads (http_config headers) can
// legitimately be larger than the tight auth-endpoint bound.
func decodeBoundedJSON(w http.ResponseWriter, r *http.Request, v any, maxBytes int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return false
	}
	return true
}

// parseBoundedInt parses s as a decimal integer directly into int32 (rather
// than strconv.Atoi + a narrowing cast) so a huge input string can't wrap
// around during conversion before the range check ever runs.
func parseBoundedInt(s string, min, max int32) (int32, error) {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, err
	}
	if int32(n) < min || int32(n) > max {
		return 0, errors.New("out of range")
	}
	return int32(n), nil
}
