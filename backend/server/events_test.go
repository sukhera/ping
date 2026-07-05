package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sukhera/ping/store"
)

// pause establishes ownership via GetMonitor, then calls the store's
// PauseMonitor and returns 200 with the updated monitor.
func TestPauseHandler_OwnershipThenMutate(t *testing.T) {
	var paused bool
	fs := &fakeMonitorStore{
		getMonitorFn: func(_ context.Context, id, _ string) (store.Monitor, error) {
			return store.Monitor{ID: id, Kind: "heartbeat"}, nil
		},
		pauseMonitorFn: func(_ context.Context, id, _ string) (store.Monitor, error) {
			paused = true
			now := time.Now()
			return store.Monitor{ID: id, Kind: "heartbeat", State: "up", DisplayState: "paused", PausedAt: &now}, nil
		},
	}
	h := newMonitorHandler(fs, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/monitors/m1/pause", nil)
	req = withAuthedUser(withChiURLParam(req, "id", "m1"), "user-1")
	rec := httptest.NewRecorder()
	h.pause(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !paused {
		t.Error("PauseMonitor not called")
	}
	var resp monitorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DisplayState != "paused" {
		t.Errorf("display_state = %q, want paused", resp.DisplayState)
	}
}

// A pause on a foreign monitor returns the GetMonitor error (403) without
// calling the mutation.
func TestPauseHandler_ForeignReturnsErrorWithoutMutating(t *testing.T) {
	fs := &fakeMonitorStore{
		getMonitorFn: func(context.Context, string, string) (store.Monitor, error) {
			return store.Monitor{}, errForbiddenForTest{}
		},
		pauseMonitorFn: func(context.Context, string, string) (store.Monitor, error) {
			t.Fatal("PauseMonitor must not be called when ownership fails")
			return store.Monitor{}, nil
		},
	}
	h := newMonitorHandler(fs, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/monitors/m1/pause", nil)
	req = withAuthedUser(withChiURLParam(req, "id", "m1"), "user-2")
	rec := httptest.NewRecorder()
	h.pause(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestResumeHandler_PassesClockToStore(t *testing.T) {
	var called bool
	fs := &fakeMonitorStore{
		getMonitorFn: func(_ context.Context, id, _ string) (store.Monitor, error) {
			return store.Monitor{ID: id, Kind: "heartbeat"}, nil
		},
		resumeMonitorFn: func(_ context.Context, id, _ string, now time.Time) (store.Monitor, error) {
			called = true
			if now.IsZero() {
				t.Error("resume called with zero clock")
			}
			return store.Monitor{ID: id, Kind: "heartbeat", State: "up"}, nil
		},
	}
	h := newMonitorHandler(fs, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/monitors/m1/resume", nil)
	req = withAuthedUser(withChiURLParam(req, "id", "m1"), "user-1")
	rec := httptest.NewRecorder()
	h.resume(rec, req)

	if rec.Code != http.StatusOK || !called {
		t.Fatalf("status=%d called=%v, want 200 + called", rec.Code, called)
	}
}

func TestListEventsHandler_PassesFiltersToStore(t *testing.T) {
	var gotMonitor, gotType, gotCursor string
	var gotLimit int32
	fs := &fakeMonitorStore{
		listEventsByUserFn: func(_ context.Context, _, monitorID, eventType, cursor string, limit int32) (store.EventPage, error) {
			gotMonitor, gotType, gotCursor, gotLimit = monitorID, eventType, cursor, limit
			return store.EventPage{Events: []store.Event{{ID: 1, Type: "pause"}}, NextCursor: "abc"}, nil
		},
	}
	h := newMonitorHandler(fs, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/events?monitor=m1&type=pause&cursor=cur&limit=5", nil)
	req = withAuthedUser(req, "user-1")
	rec := httptest.NewRecorder()
	h.listEvents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotMonitor != "m1" || gotType != "pause" || gotCursor != "cur" || gotLimit != 5 {
		t.Errorf("filters = (%q,%q,%q,%d), want (m1,pause,cur,5)", gotMonitor, gotType, gotCursor, gotLimit)
	}
	var resp eventListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Events) != 1 || resp.NextCursor != "abc" {
		t.Errorf("resp = %+v, want 1 event + cursor abc", resp)
	}
}

func TestListEventsHandler_InvalidLimit400(t *testing.T) {
	fs := &fakeMonitorStore{
		listEventsByUserFn: func(context.Context, string, string, string, string, int32) (store.EventPage, error) {
			t.Fatal("store must not be called on invalid limit")
			return store.EventPage{}, nil
		},
	}
	h := newMonitorHandler(fs, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/events?limit=abc", nil)
	req = withAuthedUser(req, "user-1")
	rec := httptest.NewRecorder()
	h.listEvents(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// listMonitorCheckins establishes ownership via GetMonitor (403 vs 404)
// before reading the check-in log, mirroring listMonitorEvents.
func TestListMonitorCheckinsHandler_ForeignMonitorReturns403(t *testing.T) {
	fs := &fakeMonitorStore{
		getMonitorFn: func(ctx context.Context, id, callerUserID string) (store.Monitor, error) {
			return store.Monitor{}, errForbiddenForTest{}
		},
		listCheckinsByMonitorFn: func(context.Context, string, string, int32) (store.CheckinPage, error) {
			t.Fatal("store must not be called when ownership check fails")
			return store.CheckinPage{}, nil
		},
	}
	h := newMonitorHandler(fs, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/monitors/m-1/checkins", nil)
	req = withAuthedUser(req, "user-2")
	req = withChiURLParam(req, "id", "m-1")
	rec := httptest.NewRecorder()
	h.listMonitorCheckins(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestListMonitorCheckinsHandler_PassesCursorAndLimitToStore(t *testing.T) {
	var gotMonitor, gotCursor string
	var gotLimit int32
	ip := "203.0.113.7"
	ua := "curl/8.0"
	body := "<script>alert(1)</script>"
	fs := &fakeMonitorStore{
		getMonitorFn: func(ctx context.Context, id, callerUserID string) (store.Monitor, error) {
			return store.Monitor{ID: id, UserID: callerUserID}, nil
		},
		listCheckinsByMonitorFn: func(_ context.Context, monitorID, cursor string, limit int32) (store.CheckinPage, error) {
			gotMonitor, gotCursor, gotLimit = monitorID, cursor, limit
			return store.CheckinPage{
				Checkins: []store.Checkin{{
					ID: 1, MonitorID: monitorID, Kind: "success",
					SourceIP: &ip, UserAgent: &ua, Body: &body,
				}},
				NextCursor: "next-abc",
			}, nil
		},
	}
	h := newMonitorHandler(fs, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/monitors/m-1/checkins?cursor=cur&limit=5", nil)
	req = withAuthedUser(req, "user-1")
	req = withChiURLParam(req, "id", "m-1")
	rec := httptest.NewRecorder()
	h.listMonitorCheckins(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotMonitor != "m-1" || gotCursor != "cur" || gotLimit != 5 {
		t.Errorf("args = (%q,%q,%d), want (m-1,cur,5)", gotMonitor, gotCursor, gotLimit)
	}

	var resp checkinListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Checkins) != 1 || resp.NextCursor != "next-abc" {
		t.Fatalf("resp = %+v, want 1 checkin + cursor next-abc", resp)
	}
	c := resp.Checkins[0]
	if c.SourceIP != ip || c.UserAgent != ua || c.Body != body {
		t.Errorf("checkin = %+v, want source_ip/user_agent/body passed through as raw text", c)
	}
}

func TestListMonitorCheckinsHandler_InvalidLimit400(t *testing.T) {
	fs := &fakeMonitorStore{
		getMonitorFn: func(ctx context.Context, id, callerUserID string) (store.Monitor, error) {
			return store.Monitor{ID: id, UserID: callerUserID}, nil
		},
		listCheckinsByMonitorFn: func(context.Context, string, string, int32) (store.CheckinPage, error) {
			t.Fatal("store must not be called on invalid limit")
			return store.CheckinPage{}, nil
		},
	}
	h := newMonitorHandler(fs, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/monitors/m-1/checkins?limit=abc", nil)
	req = withAuthedUser(req, "user-1")
	req = withChiURLParam(req, "id", "m-1")
	rec := httptest.NewRecorder()
	h.listMonitorCheckins(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
