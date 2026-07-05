//go:build integration && e2e

package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/sukhera/ping/server"
)

// TestAdvanceClock_CrossesHeartbeatDeadline exercises the actual behavior
// behind PING-022's time-warp endpoint: advancing the clock past a heartbeat
// monitor's period+grace must transition it up -> late -> down and record
// exactly one "down" event, synchronously (no worker role running), matching
// what the e2e CI job's --role=api-only backend provides.
func TestAdvanceClock_CrossesHeartbeatDeadline(t *testing.T) {
	deps := heartbeatMonitorDeps(t)
	deps.Env = "test"
	srv := server.New(":0", deps)
	token, _ := registerUser(t, srv)

	createBody := `{"kind":"heartbeat","name":"deadline test","schedule_kind":"period","period_s":60,"tz":"UTC","grace_s":60}`
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

	pingRec := doJSON(t, srv, http.MethodPost, "/p/"+slug, "", nil)
	if pingRec.Code != http.StatusOK {
		t.Fatalf("ping status = %d, want 200; body = %s", pingRec.Code, pingRec.Body.String())
	}

	rec = doAuthedJSON(t, srv, http.MethodGet, "/api/v1/monitors/"+id, "", token)
	var afterPing map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&afterPing); err != nil {
		t.Fatalf("decode monitor: %v", err)
	}
	if afterPing["state"] != "up" {
		t.Fatalf("state after ping = %v, want up", afterPing["state"])
	}

	// period_s(60) + grace_s(60) = 120s to cross both the late and down
	// thresholds in a single advance; the handler drives the scheduler pass itself.
	advRec := doJSON(t, srv, http.MethodPost, "/test/advance-clock", `{"seconds":125}`, nil)
	if advRec.Code != http.StatusOK {
		t.Fatalf("advance-clock status = %d, want 200; body = %s", advRec.Code, advRec.Body.String())
	}

	rec = doAuthedJSON(t, srv, http.MethodGet, "/api/v1/monitors/"+id, "", token)
	var afterAdvance map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&afterAdvance); err != nil {
		t.Fatalf("decode monitor: %v", err)
	}
	if afterAdvance["state"] != "down" {
		t.Fatalf("state after advance = %v, want down", afterAdvance["state"])
	}

	rec = doAuthedJSON(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/monitors/%s/events", id), "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("events status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var events struct {
		Events []struct {
			Type string `json:"type"`
		} `json:"events"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&events); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	downCount := 0
	for _, e := range events.Events {
		if e.Type == "down" {
			downCount++
		}
	}
	if downCount != 1 {
		t.Fatalf("down events = %d, want exactly 1 (got %+v)", downCount, events.Events)
	}
}
