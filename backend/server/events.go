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
