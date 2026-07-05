package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sukhera/ping/store"
)

// eventResponse is one timeline event in a feed response. Meta is the raw JSONB
// payload (e.g. config_change's changed-field list), passed through as-is.
type eventResponse struct {
	ID        int64           `json:"id"`
	MonitorID string          `json:"monitor_id"`
	Type      string          `json:"type"`
	Message   string          `json:"message"`
	Meta      json.RawMessage `json:"meta,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type eventListResponse struct {
	Events     []eventResponse `json:"events"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

func toEventResponse(e store.Event) eventResponse {
	return eventResponse{
		ID:        e.ID,
		MonitorID: e.MonitorID,
		Type:      e.Type,
		Message:   e.Message,
		Meta:      json.RawMessage(e.Meta),
		CreatedAt: e.CreatedAt,
	}
}

func writeEventPage(w http.ResponseWriter, page store.EventPage) {
	resp := eventListResponse{
		Events:     make([]eventResponse, len(page.Events)),
		NextCursor: page.NextCursor,
	}
	for i, e := range page.Events {
		resp.Events[i] = toEventResponse(e)
	}
	writeJSON(w, http.StatusOK, resp)
}

// pause/resume/mute/unmute establish ownership (403 vs 404) via GetMonitor —
// the mutation queries' id+user_id WHERE can't distinguish missing from foreign
// — then apply the change and return the updated monitor (200 + body, mirroring
// update). resume re-arms next_deadline from now (store handles the clock).

func (h *monitorHandler) pause(w http.ResponseWriter, r *http.Request) {
	h.mutateMonitor(w, r, func(id, userID string) (store.Monitor, error) {
		return h.store.PauseMonitor(r.Context(), id, userID)
	})
}

func (h *monitorHandler) resume(w http.ResponseWriter, r *http.Request) {
	h.mutateMonitor(w, r, func(id, userID string) (store.Monitor, error) {
		return h.store.ResumeMonitor(r.Context(), id, userID, time.Now())
	})
}

func (h *monitorHandler) mute(w http.ResponseWriter, r *http.Request) {
	h.mutateMonitor(w, r, func(id, userID string) (store.Monitor, error) {
		return h.store.MuteMonitor(r.Context(), id, userID)
	})
}

func (h *monitorHandler) unmute(w http.ResponseWriter, r *http.Request) {
	h.mutateMonitor(w, r, func(id, userID string) (store.Monitor, error) {
		return h.store.UnmuteMonitor(r.Context(), id, userID)
	})
}

// mutateMonitor is the shared pause/resume/mute/unmute flow: ownership check,
// then the mutation, then 200 + updated monitor body.
func (h *monitorHandler) mutateMonitor(w http.ResponseWriter, r *http.Request, mutate func(id, userID string) (store.Monitor, error)) {
	id := chi.URLParam(r, "id")
	userID := userIDFromContext(r.Context())

	if _, err := h.store.GetMonitor(r.Context(), id, userID); err != nil {
		writeError(w, r, err)
		return
	}

	m, err := mutate(id, userID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toMonitorResponse(m, h.deps.BaseURL))
}

// listEvents is the global feed: the caller's events across all their monitors,
// filterable by ?monitor and ?type, cursor-paginated by ?cursor/?limit.
func (h *monitorHandler) listEvents(w http.ResponseWriter, r *http.Request) {
	limit, ok := eventPageLimit(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	page, err := h.store.ListEventsByUser(
		r.Context(),
		userIDFromContext(r.Context()),
		q.Get("monitor"),
		q.Get("type"),
		q.Get("cursor"),
		limit,
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeEventPage(w, page)
}

// listMonitorEvents is the per-monitor feed. Ownership is established via
// GetMonitor (403 vs 404) before reading the feed.
func (h *monitorHandler) listMonitorEvents(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.store.GetMonitor(r.Context(), id, userIDFromContext(r.Context())); err != nil {
		writeError(w, r, err)
		return
	}

	limit, ok := eventPageLimit(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	page, err := h.store.ListEventsByMonitor(r.Context(), id, q.Get("type"), q.Get("cursor"), limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeEventPage(w, page)
}

// eventPageLimit reads ?limit (default defaultPageLimit, max maxPageLimit),
// writing a 400 and returning ok=false on a malformed value.
func eventPageLimit(w http.ResponseWriter, r *http.Request) (int32, bool) {
	limit := int32(defaultPageLimit)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := parseBoundedInt(raw, 1, maxPageLimit)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
			return 0, false
		}
		limit = n
	}
	return limit, true
}

// checkinResponse is one check-in log row (PING-014 DESIGN.md §7.2). Body is
// passed through as raw text — the frontend renders it via React's default
// text escaping (never dangerouslySetInnerHTML), so an HTML/script payload
// stays inert on screen without any server-side sanitization needed.
type checkinResponse struct {
	ID        int64     `json:"id"`
	MonitorID string    `json:"monitor_id"`
	Kind      string    `json:"kind"`
	SourceIP  string    `json:"source_ip,omitempty"`
	UserAgent string    `json:"user_agent,omitempty"`
	Body      string    `json:"body,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type checkinListResponse struct {
	Checkins   []checkinResponse `json:"checkins"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

func toCheckinResponse(c store.Checkin) checkinResponse {
	resp := checkinResponse{
		ID:        c.ID,
		MonitorID: c.MonitorID,
		Kind:      c.Kind,
		CreatedAt: c.CreatedAt,
	}
	if c.SourceIP != nil {
		resp.SourceIP = *c.SourceIP
	}
	if c.UserAgent != nil {
		resp.UserAgent = *c.UserAgent
	}
	if c.Body != nil {
		resp.Body = *c.Body
	}
	return resp
}

// listMonitorCheckins is the per-monitor check-in log (PING-014). Ownership
// is established via GetMonitor (403 vs 404) before reading the log, same as
// listMonitorEvents.
func (h *monitorHandler) listMonitorCheckins(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.store.GetMonitor(r.Context(), id, userIDFromContext(r.Context())); err != nil {
		writeError(w, r, err)
		return
	}

	limit, ok := eventPageLimit(w, r)
	if !ok {
		return
	}
	page, err := h.store.ListCheckinsByMonitor(r.Context(), id, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := checkinListResponse{
		Checkins:   make([]checkinResponse, len(page.Checkins)),
		NextCursor: page.NextCursor,
	}
	for i, c := range page.Checkins {
		resp.Checkins[i] = toCheckinResponse(c)
	}
	writeJSON(w, http.StatusOK, resp)
}

// probeResultResponse is one probe log row (PING-018 DESIGN.md §7.2 HTTP
// detail view).
type probeResultResponse struct {
	ID           int64      `json:"id"`
	MonitorID    string     `json:"monitor_id"`
	OK           bool       `json:"ok"`
	HTTPStatus   *int32     `json:"http_status,omitempty"`
	LatencyMS    *int32     `json:"latency_ms,omitempty"`
	Error        string     `json:"error,omitempty"`
	TLSExpiresAt *time.Time `json:"tls_expires_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

type probeResultListResponse struct {
	Results    []probeResultResponse `json:"results"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

func toProbeResultResponse(p store.ProbeResult) probeResultResponse {
	return probeResultResponse{
		ID:           p.ID,
		MonitorID:    p.MonitorID,
		OK:           p.OK,
		HTTPStatus:   p.HTTPStatus,
		LatencyMS:    p.LatencyMS,
		Error:        p.Error,
		TLSExpiresAt: p.TLSExpiresAt,
		CreatedAt:    p.CreatedAt,
	}
}

// listMonitorProbeResults is the HTTP monitor probe log (PING-018): ownership
// via GetMonitor, cursor pagination, optional ?outcome=success|fail filter —
// same shape as listMonitorCheckins.
func (h *monitorHandler) listMonitorProbeResults(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.store.GetMonitor(r.Context(), id, userIDFromContext(r.Context())); err != nil {
		writeError(w, r, err)
		return
	}

	outcome := r.URL.Query().Get("outcome")
	if outcome != "" && outcome != "success" && outcome != "fail" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid outcome"})
		return
	}

	limit, ok := eventPageLimit(w, r)
	if !ok {
		return
	}
	page, err := h.store.ListProbeResultsByMonitor(r.Context(), id, outcome, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := probeResultListResponse{
		Results:    make([]probeResultResponse, len(page.Results)),
		NextCursor: page.NextCursor,
	}
	for i, p := range page.Results {
		resp.Results[i] = toProbeResultResponse(p)
	}
	writeJSON(w, http.StatusOK, resp)
}

// latencyWindows maps the ?window query param to how far back to look and
// what bucket width to aggregate at, so the point count stays chart-sized
// regardless of window length (~288 points for 24h, ~168 for 7d, ~120 for
// 30d).
var latencyWindows = map[string]struct {
	lookback time.Duration
	bucket   int32
}{
	"24h": {24 * time.Hour, 5 * 60},
	"7d":  {7 * 24 * time.Hour, 60 * 60},
	"30d": {30 * 24 * time.Hour, 6 * 60 * 60},
}

type latencyPointResponse struct {
	BucketStart time.Time `json:"bucket_start"`
	P50         float64   `json:"p50"`
	P95         float64   `json:"p95"`
	Avg         float64   `json:"avg"`
	SampleCount int64     `json:"sample_count"`
}

type latencySeriesResponse struct {
	Window string                 `json:"window"`
	Points []latencyPointResponse `json:"points"`
}

// getMonitorLatencySeries is the latency chart's backing endpoint (PING-018):
// ?window=24h|7d|30d (default 24h), pre-bucketed p50/p95/avg per point.
// Ownership via GetMonitor, same pattern as the other per-monitor feeds.
func (h *monitorHandler) getMonitorLatencySeries(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.store.GetMonitor(r.Context(), id, userIDFromContext(r.Context())); err != nil {
		writeError(w, r, err)
		return
	}

	window := r.URL.Query().Get("window")
	if window == "" {
		window = "24h"
	}
	w24, ok := latencyWindows[window]
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid window"})
		return
	}

	since := time.Now().Add(-w24.lookback)
	buckets, err := h.store.LatencySeriesByMonitor(r.Context(), id, since, w24.bucket)
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := latencySeriesResponse{Window: window, Points: make([]latencyPointResponse, len(buckets))}
	for i, b := range buckets {
		resp.Points[i] = latencyPointResponse{
			BucketStart: b.BucketStart,
			P50:         b.P50,
			P95:         b.P95,
			Avg:         b.Avg,
			SampleCount: b.SampleCount,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
