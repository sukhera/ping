//go:build integration

package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sukhera/ping/server"
)

// registerUser creates a fresh account against srv and returns its access
// token, for use as an Authorization: Bearer header in monitor tests.
func registerUser(t *testing.T, srv *http.Server) (accessToken, userID string) {
	t.Helper()
	body := fmt.Sprintf(`{"email":%q,"password":"correcthorsebatterystaple"}`, uniqueEmail(t))
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/auth/register", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		AccessToken string `json:"access_token"`
		User        struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	return resp.AccessToken, resp.User.ID
}

// doAuthedJSON is doJSON (auth_integration_test.go) plus an Authorization:
// Bearer header, since monitor routes use header auth, not the refresh
// cookie the auth-flow tests exercise.
func doAuthedJSON(t *testing.T, srv *http.Server, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequestWithContext(context.Background(), method, path, nil)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

func heartbeatMonitorDeps(t *testing.T) server.Deps {
	t.Helper()
	deps := testAuthDeps(t, true)
	deps.BaseURL = "https://ping.example.com"
	return deps
}

func TestMonitor_CreateGetListUpdateDelete_HeartbeatRoundTrip(t *testing.T) {
	deps := heartbeatMonitorDeps(t)
	srv := server.New(":0", deps)
	token, _ := registerUser(t, srv)

	createBody := `{"kind":"heartbeat","name":"nightly backup","schedule_kind":"period","period_s":300,"tz":"UTC","grace_s":60}`
	rec := doAuthedJSON(t, srv, http.MethodPost, "/api/v1/monitors", createBody, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	id, _ := created["id"].(string)
	slug, _ := created["slug"].(string)
	if id == "" || slug == "" {
		t.Fatalf("expected non-empty id and slug, got %+v", created)
	}
	wantPingURL := "https://ping.example.com/p/" + slug
	if created["ping_url"] != wantPingURL {
		t.Errorf("ping_url = %v, want %q", created["ping_url"], wantPingURL)
	}

	rec = doAuthedJSON(t, srv, http.MethodGet, "/api/v1/monitors/"+id, "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	rec = doAuthedJSON(t, srv, http.MethodGet, "/api/v1/monitors/", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var listResp struct {
		Monitors []map[string]any `json:"monitors"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Monitors) != 1 {
		t.Fatalf("expected 1 monitor in list, got %d", len(listResp.Monitors))
	}

	rec = doAuthedJSON(t, srv, http.MethodPatch, "/api/v1/monitors/"+id, `{"name":"renamed backup"}`, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var updated map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated["name"] != "renamed backup" {
		t.Errorf("name after update = %v, want renamed backup", updated["name"])
	}
	if updated["slug"] != slug {
		t.Error("expected slug to be unchanged by a name-only update")
	}

	rec = doAuthedJSON(t, srv, http.MethodDelete, "/api/v1/monitors/"+id, "", token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	rec = doAuthedJSON(t, srv, http.MethodGet, "/api/v1/monitors/"+id, "", token)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get-after-delete status = %d, want 404", rec.Code)
	}
}

func TestMonitor_CreateGetDelete_HTTPRoundTrip(t *testing.T) {
	deps := heartbeatMonitorDeps(t)
	srv := server.New(":0", deps)
	token, _ := registerUser(t, srv)

	createBody := `{"kind":"http","name":"api check","url":"https://example.com/health","method":"GET","interval_s":60,"timeout_s":10}`
	rec := doAuthedJSON(t, srv, http.MethodPost, "/api/v1/monitors", createBody, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	// http monitors have no ping URL — they're actively probed, not passively pinged.
	if created["ping_url"] != nil {
		t.Errorf("ping_url = %v, want absent for an http monitor", created["ping_url"])
	}
}

func TestMonitor_InvalidScheduleReturns422WithFieldDetail(t *testing.T) {
	deps := heartbeatMonitorDeps(t)
	srv := server.New(":0", deps)
	token, _ := registerUser(t, srv)

	// period_s of 10 is below schedule.MinPeriod (1 minute).
	createBody := `{"kind":"heartbeat","name":"too fast","schedule_kind":"period","period_s":10,"tz":"UTC","grace_s":60}`
	rec := doAuthedJSON(t, srv, http.MethodPost, "/api/v1/monitors", createBody, token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["field"] != "period_s" {
		t.Errorf("field = %q, want period_s", resp["field"])
	}
}

func TestMonitor_NoTokenReturns401(t *testing.T) {
	deps := heartbeatMonitorDeps(t)
	srv := server.New(":0", deps)

	rec := doJSON(t, srv, http.MethodGet, "/api/v1/monitors/", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestMonitor_ForeignOwnerReturns403NotFoundMasking is the IDOR
// acceptance-criterion test: user B must get 403 on user A's monitor, not a
// 404 that would mask its existence.
func TestMonitor_ForeignOwnerReturns403NotFoundMasking(t *testing.T) {
	deps := heartbeatMonitorDeps(t)
	srv := server.New(":0", deps)
	tokenA, _ := registerUser(t, srv)
	tokenB, _ := registerUser(t, srv)

	createBody := `{"kind":"heartbeat","name":"user A's monitor","schedule_kind":"period","period_s":300,"tz":"UTC","grace_s":60}`
	rec := doAuthedJSON(t, srv, http.MethodPost, "/api/v1/monitors", createBody, tokenA)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	id, _ := created["id"].(string)

	rec = doAuthedJSON(t, srv, http.MethodGet, "/api/v1/monitors/"+id, "", tokenB)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("get status = %d, want 403 (not 404-masking)", rec.Code)
	}

	rec = doAuthedJSON(t, srv, http.MethodPatch, "/api/v1/monitors/"+id, `{"name":"hijacked"}`, tokenB)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("update status = %d, want 403", rec.Code)
	}

	rec = doAuthedJSON(t, srv, http.MethodDelete, "/api/v1/monitors/"+id, "", tokenB)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("delete status = %d, want 403", rec.Code)
	}
}

// TestMonitor_ListPagination_StableUnderConcurrentInsert is the pagination
// acceptance-criterion test: a monitor inserted between fetching page 1 and
// page 2 must not shift already-fetched results or produce a duplicate.
func TestMonitor_ListPagination_StableUnderConcurrentInsert(t *testing.T) {
	deps := heartbeatMonitorDeps(t)
	srv := server.New(":0", deps)
	token, _ := registerUser(t, srv)

	createOne := func(name string) {
		body := fmt.Sprintf(`{"kind":"heartbeat","name":%q,"schedule_kind":"period","period_s":300,"tz":"UTC","grace_s":60}`, name)
		rec := doAuthedJSON(t, srv, http.MethodPost, "/api/v1/monitors", body, token)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %q status = %d, want 201; body = %s", name, rec.Code, rec.Body.String())
		}
	}
	for i := range 3 {
		createOne(fmt.Sprintf("monitor-%d", i))
	}

	rec := doAuthedJSON(t, srv, http.MethodGet, "/api/v1/monitors/?limit=2", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("page 1 status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var page1 struct {
		Monitors   []map[string]any `json:"monitors"`
		NextCursor string           `json:"next_cursor"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&page1); err != nil {
		t.Fatalf("decode page 1: %v", err)
	}
	if len(page1.Monitors) != 2 || page1.NextCursor == "" {
		t.Fatalf("page 1 = %+v, want 2 monitors with a next_cursor", page1)
	}

	// Insert a new monitor "between" pages — it should land at the front
	// (newest first) and not appear on, or shift, page 2.
	createOne("inserted-between-pages")

	rec = doAuthedJSON(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/monitors/?limit=2&cursor=%s", page1.NextCursor), "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("page 2 status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var page2 struct {
		Monitors []map[string]any `json:"monitors"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&page2); err != nil {
		t.Fatalf("decode page 2: %v", err)
	}
	if len(page2.Monitors) != 1 {
		t.Fatalf("page 2 = %+v, want exactly the 1 remaining original monitor", page2)
	}
	for _, m := range page2.Monitors {
		if m["name"] == "inserted-between-pages" {
			t.Error("concurrently inserted monitor leaked into page 2 — cursor pagination is not stable")
		}
	}
	seen := map[string]bool{}
	for _, m := range append(page1.Monitors, page2.Monitors...) {
		id, _ := m["id"].(string)
		if seen[id] {
			t.Errorf("monitor %s appeared on both pages", id)
		}
		seen[id] = true
	}
}

func TestDescribeSchedule_Integration(t *testing.T) {
	deps := heartbeatMonitorDeps(t)
	srv := server.New(":0", deps)
	token, _ := registerUser(t, srv)

	body := `{"schedule_kind":"cron","cron_expr":"0 4 * * *","tz":"Europe/Berlin","grace_s":1800}`
	rec := doAuthedJSON(t, srv, http.MethodPost, "/api/v1/schedule/describe", body, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Description string `json:"description"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := "every day at 04:00 (Europe/Berlin); alert if 30 min late"
	if resp.Description != want {
		t.Errorf("description = %q, want %q", resp.Description, want)
	}
}
