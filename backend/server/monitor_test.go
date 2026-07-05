package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sukhera/ping/store"
)

type fakeMonitorStore struct {
	createMonitorFn func(ctx context.Context, p store.CreateMonitorParams) (store.Monitor, error)
	getMonitorFn    func(ctx context.Context, id, callerUserID string) (store.Monitor, error)
	listMonitorsFn  func(ctx context.Context, userID, cursor string, limit int32, filter store.ListMonitorsFilter) (store.MonitorPage, error)
	updateMonitorFn func(ctx context.Context, id, callerUserID string, p store.UpdateMonitorParams) (store.Monitor, error)
	deleteMonitorFn func(ctx context.Context, id, callerUserID string) error

	pauseMonitorFn        func(ctx context.Context, id, callerUserID string) (store.Monitor, error)
	resumeMonitorFn       func(ctx context.Context, id, callerUserID string, now time.Time) (store.Monitor, error)
	muteMonitorFn         func(ctx context.Context, id, callerUserID string) (store.Monitor, error)
	unmuteMonitorFn       func(ctx context.Context, id, callerUserID string) (store.Monitor, error)
	listEventsByUserFn    func(ctx context.Context, userID, monitorID, eventType, cursor string, limit int32) (store.EventPage, error)
	listEventsByMonitorFn func(ctx context.Context, monitorID, eventType, cursor string, limit int32) (store.EventPage, error)
	listDailyStatsFn      func(ctx context.Context, monitorIDs []string, since time.Time) (map[string][]store.DailyStat, error)

	listCheckinsByMonitorFn func(ctx context.Context, monitorID, cursor string, limit int32) (store.CheckinPage, error)
}

func (f *fakeMonitorStore) CreateMonitor(ctx context.Context, p store.CreateMonitorParams) (store.Monitor, error) {
	return f.createMonitorFn(ctx, p)
}
func (f *fakeMonitorStore) GetMonitor(ctx context.Context, id, callerUserID string) (store.Monitor, error) {
	return f.getMonitorFn(ctx, id, callerUserID)
}
func (f *fakeMonitorStore) ListMonitors(ctx context.Context, userID, cursor string, limit int32, filter store.ListMonitorsFilter) (store.MonitorPage, error) {
	return f.listMonitorsFn(ctx, userID, cursor, limit, filter)
}
func (f *fakeMonitorStore) ListDailyStats(ctx context.Context, monitorIDs []string, since time.Time) (map[string][]store.DailyStat, error) {
	if f.listDailyStatsFn == nil {
		return map[string][]store.DailyStat{}, nil
	}
	return f.listDailyStatsFn(ctx, monitorIDs, since)
}
func (f *fakeMonitorStore) UpdateMonitor(ctx context.Context, id, callerUserID string, p store.UpdateMonitorParams) (store.Monitor, error) {
	return f.updateMonitorFn(ctx, id, callerUserID, p)
}
func (f *fakeMonitorStore) DeleteMonitor(ctx context.Context, id, callerUserID string) error {
	return f.deleteMonitorFn(ctx, id, callerUserID)
}
func (f *fakeMonitorStore) PauseMonitor(ctx context.Context, id, callerUserID string) (store.Monitor, error) {
	return f.pauseMonitorFn(ctx, id, callerUserID)
}
func (f *fakeMonitorStore) ResumeMonitor(ctx context.Context, id, callerUserID string, now time.Time) (store.Monitor, error) {
	return f.resumeMonitorFn(ctx, id, callerUserID, now)
}
func (f *fakeMonitorStore) MuteMonitor(ctx context.Context, id, callerUserID string) (store.Monitor, error) {
	return f.muteMonitorFn(ctx, id, callerUserID)
}
func (f *fakeMonitorStore) UnmuteMonitor(ctx context.Context, id, callerUserID string) (store.Monitor, error) {
	return f.unmuteMonitorFn(ctx, id, callerUserID)
}
func (f *fakeMonitorStore) ListEventsByUser(ctx context.Context, userID, monitorID, eventType, cursor string, limit int32) (store.EventPage, error) {
	return f.listEventsByUserFn(ctx, userID, monitorID, eventType, cursor, limit)
}
func (f *fakeMonitorStore) ListEventsByMonitor(ctx context.Context, monitorID, eventType, cursor string, limit int32) (store.EventPage, error) {
	return f.listEventsByMonitorFn(ctx, monitorID, eventType, cursor, limit)
}
func (f *fakeMonitorStore) ListCheckinsByMonitor(ctx context.Context, monitorID, cursor string, limit int32) (store.CheckinPage, error) {
	if f.listCheckinsByMonitorFn == nil {
		return store.CheckinPage{}, nil
	}
	return f.listCheckinsByMonitorFn(ctx, monitorID, cursor, limit)
}

// withChiURLParam and withAuthedUser build a request as if it had already
// passed through chi's router (for {id}) and requireAuth (for the user ID),
// since these are handler-level tests that call handlers directly.
func withChiURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func withAuthedUser(r *http.Request, userID string) *http.Request {
	return r.WithContext(withUserID(r.Context(), userID))
}

func TestMonitorCreate_MissingNameReturns422(t *testing.T) {
	h := newMonitorHandler(&fakeMonitorStore{}, testDeps(t))

	body := `{"kind":"heartbeat","schedule_kind":"period","period_s":300,"tz":"UTC","grace_s":60}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/monitors", strings.NewReader(body))
	req = withAuthedUser(req, "user-1")
	rec := httptest.NewRecorder()
	h.create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	var resp fieldErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Field != "name" {
		t.Errorf("field = %q, want name", resp.Field)
	}
}

func TestMonitorCreate_UnknownKindReturns422(t *testing.T) {
	h := newMonitorHandler(&fakeMonitorStore{}, testDeps(t))

	body := `{"kind":"carrier-pigeon","name":"x"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/monitors", strings.NewReader(body))
	req = withAuthedUser(req, "user-1")
	rec := httptest.NewRecorder()
	h.create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestMonitorCreate_InvalidScheduleReturns422WithFieldDetail(t *testing.T) {
	h := newMonitorHandler(&fakeMonitorStore{}, testDeps(t))

	// grace_s of 5 is below schedule.MinGrace (1 minute).
	body := `{"kind":"heartbeat","name":"nightly","schedule_kind":"period","period_s":300,"tz":"UTC","grace_s":5}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/monitors", strings.NewReader(body))
	req = withAuthedUser(req, "user-1")
	rec := httptest.NewRecorder()
	h.create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	var resp fieldErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Field != "grace_s" {
		t.Errorf("field = %q, want grace_s", resp.Field)
	}
}

func TestMonitorCreate_HeartbeatSuccessReturnsPingURL(t *testing.T) {
	fs := &fakeMonitorStore{
		createMonitorFn: func(ctx context.Context, p store.CreateMonitorParams) (store.Monitor, error) {
			if p.UserID != "user-1" {
				t.Errorf("UserID = %q, want user-1 (from context, not client-supplied)", p.UserID)
			}
			return store.Monitor{ID: "m-1", UserID: p.UserID, Kind: "heartbeat", Slug: "abc123", Name: p.Name, TZ: "UTC", DisplayState: "new"}, nil
		},
	}
	deps := testDeps(t)
	deps.BaseURL = "https://ping.example.com"
	h := newMonitorHandler(fs, deps)

	body := `{"kind":"heartbeat","name":"nightly backup","schedule_kind":"period","period_s":300,"tz":"UTC","grace_s":60}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/monitors", strings.NewReader(body))
	req = withAuthedUser(req, "user-1")
	rec := httptest.NewRecorder()
	h.create(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}
	var resp monitorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PingURL != "https://ping.example.com/p/abc123" {
		t.Errorf("ping_url = %q, want https://ping.example.com/p/abc123", resp.PingURL)
	}
}

func TestMonitorCreate_HTTPMonitorRejectsScheduleFields(t *testing.T) {
	h := newMonitorHandler(&fakeMonitorStore{}, testDeps(t))

	body := `{"kind":"http","name":"api check","url":"https://example.com","schedule_kind":"period"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/monitors", strings.NewReader(body))
	req = withAuthedUser(req, "user-1")
	rec := httptest.NewRecorder()
	h.create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestMonitorCreate_HTTPMonitorInvalidURLReturns422(t *testing.T) {
	h := newMonitorHandler(&fakeMonitorStore{}, testDeps(t))

	body := `{"kind":"http","name":"api check","url":"not-a-url"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/monitors", strings.NewReader(body))
	req = withAuthedUser(req, "user-1")
	rec := httptest.NewRecorder()
	h.create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestMonitorGet_ForeignMonitorReturns403(t *testing.T) {
	fs := &fakeMonitorStore{
		getMonitorFn: func(ctx context.Context, id, callerUserID string) (store.Monitor, error) {
			return store.Monitor{}, errForbiddenForTest{}
		},
	}
	h := newMonitorHandler(fs, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/monitors/m-1", nil)
	req = withAuthedUser(req, "user-2")
	req = withChiURLParam(req, "id", "m-1")
	rec := httptest.NewRecorder()
	h.get(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestMonitorGet_Success(t *testing.T) {
	fs := &fakeMonitorStore{
		getMonitorFn: func(ctx context.Context, id, callerUserID string) (store.Monitor, error) {
			return store.Monitor{ID: id, UserID: callerUserID, Kind: "heartbeat", Name: "mine", TZ: "UTC"}, nil
		},
	}
	h := newMonitorHandler(fs, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/monitors/m-1", nil)
	req = withAuthedUser(req, "user-1")
	req = withChiURLParam(req, "id", "m-1")
	rec := httptest.NewRecorder()
	h.get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// get attaches daily_stats (PING-014 detail page uptime bar + 7/30/90d %),
// the same data the list handler already attaches per row.
func TestMonitorGet_IncludesDailyStats(t *testing.T) {
	var gotIDs []string
	fs := &fakeMonitorStore{
		getMonitorFn: func(ctx context.Context, id, callerUserID string) (store.Monitor, error) {
			return store.Monitor{ID: id, UserID: callerUserID, Kind: "heartbeat", Name: "mine", TZ: "UTC"}, nil
		},
		listDailyStatsFn: func(_ context.Context, monitorIDs []string, _ time.Time) (map[string][]store.DailyStat, error) {
			gotIDs = monitorIDs
			return map[string][]store.DailyStat{
				"m-1": {{Day: time.Now(), Checkins: 10, Failures: 1}},
			}, nil
		},
	}
	h := newMonitorHandler(fs, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/monitors/m-1", nil)
	req = withAuthedUser(req, "user-1")
	req = withChiURLParam(req, "id", "m-1")
	rec := httptest.NewRecorder()
	h.get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(gotIDs) != 1 || gotIDs[0] != "m-1" {
		t.Errorf("ListDailyStats ids = %v, want [m-1]", gotIDs)
	}

	var resp monitorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.DailyStats) != 1 || resp.DailyStats[0].Checkins != 10 {
		t.Errorf("resp.DailyStats = %+v, want 1 row with 10 checkins", resp.DailyStats)
	}
}

func TestMonitorUpdate_EstablishesOwnershipBeforeApplying(t *testing.T) {
	getCalled := false
	fs := &fakeMonitorStore{
		getMonitorFn: func(ctx context.Context, id, callerUserID string) (store.Monitor, error) {
			getCalled = true
			return store.Monitor{}, errForbiddenForTest{}
		},
		updateMonitorFn: func(ctx context.Context, id, callerUserID string, p store.UpdateMonitorParams) (store.Monitor, error) {
			t.Fatal("UpdateMonitor should not be called when ownership check fails")
			return store.Monitor{}, nil
		},
	}
	h := newMonitorHandler(fs, testDeps(t))

	body := `{"name":"renamed"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/monitors/m-1", strings.NewReader(body))
	req = withAuthedUser(req, "user-2")
	req = withChiURLParam(req, "id", "m-1")
	rec := httptest.NewRecorder()
	h.update(rec, req)

	if !getCalled {
		t.Fatal("expected GetMonitor to be called to establish ownership")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestMonitorUpdate_RejectsHTTPFieldsOnHeartbeatMonitor(t *testing.T) {
	fs := &fakeMonitorStore{
		getMonitorFn: func(ctx context.Context, id, callerUserID string) (store.Monitor, error) {
			return store.Monitor{ID: id, UserID: callerUserID, Kind: "heartbeat", TZ: "UTC"}, nil
		},
	}
	h := newMonitorHandler(fs, testDeps(t))

	body := `{"url":"https://example.com"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/monitors/m-1", strings.NewReader(body))
	req = withAuthedUser(req, "user-1")
	req = withChiURLParam(req, "id", "m-1")
	rec := httptest.NewRecorder()
	h.update(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestMonitorUpdate_RejectsScheduleFieldsOnHTTPMonitor(t *testing.T) {
	fs := &fakeMonitorStore{
		getMonitorFn: func(ctx context.Context, id, callerUserID string) (store.Monitor, error) {
			return store.Monitor{ID: id, UserID: callerUserID, Kind: "http", URL: "https://example.com", Method: "GET"}, nil
		},
	}
	h := newMonitorHandler(fs, testDeps(t))

	body := `{"schedule_kind":"period","period_s":300}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/monitors/m-1", strings.NewReader(body))
	req = withAuthedUser(req, "user-1")
	req = withChiURLParam(req, "id", "m-1")
	rec := httptest.NewRecorder()
	h.update(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	var resp fieldErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Field != "schedule_kind" {
		t.Errorf("field = %q, want schedule_kind", resp.Field)
	}
}

func TestMonitorDelete_Success(t *testing.T) {
	fs := &fakeMonitorStore{
		getMonitorFn: func(ctx context.Context, id, callerUserID string) (store.Monitor, error) {
			return store.Monitor{ID: id, UserID: callerUserID, Kind: "heartbeat", TZ: "UTC"}, nil
		},
		deleteMonitorFn: func(ctx context.Context, id, callerUserID string) error {
			return nil
		},
	}
	h := newMonitorHandler(fs, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/monitors/m-1", nil)
	req = withAuthedUser(req, "user-1")
	req = withChiURLParam(req, "id", "m-1")
	rec := httptest.NewRecorder()
	h.delete(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}

func TestDescribeSchedule_ValidConfigReturnsDescription(t *testing.T) {
	h := newMonitorHandler(&fakeMonitorStore{}, testDeps(t))

	body := `{"schedule_kind":"cron","cron_expr":"0 4 * * *","tz":"Europe/Berlin","grace_s":1800}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/schedule/describe", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.describeSchedule(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var resp describeResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := "every day at 04:00 (Europe/Berlin); alert if 30 min late"
	if resp.Description != want {
		t.Errorf("description = %q, want %q", resp.Description, want)
	}
}

func TestDescribeSchedule_InvalidConfigReturns422(t *testing.T) {
	h := newMonitorHandler(&fakeMonitorStore{}, testDeps(t))

	body := `{"schedule_kind":"cron","cron_expr":"not a cron","tz":"UTC","grace_s":60}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/schedule/describe", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.describeSchedule(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

// errForbiddenForTest implements storeError so writeError maps it to 403,
// mirroring store.ErrForbidden's real HTTPStatus() without importing store's
// internal httpError type.
type errForbiddenForTest struct{}

func (errForbiddenForTest) Error() string   { return "forbidden" }
func (errForbiddenForTest) HTTPStatus() int { return http.StatusForbidden }

var _ error = errForbiddenForTest{}
