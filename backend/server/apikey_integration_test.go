//go:build integration

package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sukhera/ping/server"
)

// createAPIKey uses a JWT session (key management is JWT-only, PING-016) to
// mint a fresh API key with the given label, returning its plaintext token
// and id.
func createAPIKey(t *testing.T, srv *http.Server, jwt, label string) (plainKey, id string) {
	t.Helper()
	rec := doAuthedJSON(t, srv, http.MethodPost, "/api/v1/apikeys", `{"label":"`+label+`"}`, jwt)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create api key status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode create api key response: %v", err)
	}
	return resp.Key, resp.ID
}

// TestAPIKey_FullMonitorCRUDRoundTrip is the AC's "curl with a key can
// perform full monitor CRUD": everything here uses an Authorization: Bearer
// pk_... header, no JWT, on the same /api/v1/monitors routes the web UI uses.
func TestAPIKey_FullMonitorCRUDRoundTrip(t *testing.T) {
	deps := heartbeatMonitorDeps(t)
	srv := server.New(":0", deps)
	jwt, _ := registerUser(t, srv)
	apiKey, _ := createAPIKey(t, srv, jwt, "curl access")

	createBody := `{"kind":"heartbeat","name":"api-key monitor","schedule_kind":"period","period_s":300,"tz":"UTC","grace_s":60}`
	rec := doAuthedJSON(t, srv, http.MethodPost, "/api/v1/monitors", createBody, apiKey)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("expected non-empty id, got %+v", created)
	}

	rec = doAuthedJSON(t, srv, http.MethodGet, "/api/v1/monitors/"+id, "", apiKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	rec = doAuthedJSON(t, srv, http.MethodGet, "/api/v1/monitors", "", apiKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	rec = doAuthedJSON(t, srv, http.MethodPatch, "/api/v1/monitors/"+id, `{"name":"renamed via api key"}`, apiKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	rec = doAuthedJSON(t, srv, http.MethodDelete, "/api/v1/monitors/"+id, "", apiKey)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
}

// TestAPIKey_RevokedKeyRejectedOnNextRequest is the AC's "revoked key -> 401
// within one request (no cache window)" exercised through the real HTTP
// router, not just the store layer.
func TestAPIKey_RevokedKeyRejectedOnNextRequest(t *testing.T) {
	deps := heartbeatMonitorDeps(t)
	srv := server.New(":0", deps)
	jwt, _ := registerUser(t, srv)
	apiKey, keyID := createAPIKey(t, srv, jwt, "to be revoked")

	rec := doAuthedJSON(t, srv, http.MethodGet, "/api/v1/monitors", "", apiKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("list before revoke status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	rec = doAuthedJSON(t, srv, http.MethodDelete, "/api/v1/apikeys/"+keyID, "", jwt)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	rec = doAuthedJSON(t, srv, http.MethodGet, "/api/v1/monitors", "", apiKey)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("list after revoke status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

// TestAPIKey_CannotManageOtherKeys confirms the JWT-only design decision: a
// pk_... token authenticates the monitor CRUD surface but is rejected on the
// key-management routes, so a leaked key can't mint or revoke other keys.
func TestAPIKey_CannotManageOtherKeys(t *testing.T) {
	deps := heartbeatMonitorDeps(t)
	srv := server.New(":0", deps)
	jwt, _ := registerUser(t, srv)
	apiKey, _ := createAPIKey(t, srv, jwt, "no key management")

	rec := doAuthedJSON(t, srv, http.MethodGet, "/api/v1/apikeys", "", apiKey)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("list api keys with a pk_ token status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}

	rec = doAuthedJSON(t, srv, http.MethodPost, "/api/v1/apikeys", `{"label":"minted by a key"}`, apiKey)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("create api key with a pk_ token status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestAPIKey_ListShowsLastUsedAtAfterUse(t *testing.T) {
	deps := heartbeatMonitorDeps(t)
	srv := server.New(":0", deps)
	jwt, _ := registerUser(t, srv)
	apiKey, _ := createAPIKey(t, srv, jwt, "last used check")

	rec := doAuthedJSON(t, srv, http.MethodGet, "/api/v1/apikeys", "", jwt)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var before []struct {
		LastUsedAt *string `json:"last_used_at"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&before); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(before) != 1 || before[0].LastUsedAt != nil {
		t.Fatalf("expected one key with nil last_used_at before use, got %+v", before)
	}

	rec = doAuthedJSON(t, srv, http.MethodGet, "/api/v1/monitors", "", apiKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("monitors list with api key status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	rec = doAuthedJSON(t, srv, http.MethodGet, "/api/v1/apikeys", "", jwt)
	if rec.Code != http.StatusOK {
		t.Fatalf("list after use status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var after []struct {
		LastUsedAt *string `json:"last_used_at"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&after); err != nil {
		t.Fatalf("decode list response after use: %v", err)
	}
	if len(after) != 1 || after[0].LastUsedAt == nil {
		t.Fatalf("expected last_used_at to be set after use, got %+v", after)
	}
}

// TestAPIKey_ListShowsRevokedAt confirms a revoked key stays visible in the
// list (never silently disappears — the user needs an audit trail of past
// keys) but carries a non-nil revoked_at so the settings UI can render it as
// revoked instead of leaving a live-looking Revoke action on a dead key.
func TestAPIKey_ListShowsRevokedAt(t *testing.T) {
	deps := heartbeatMonitorDeps(t)
	srv := server.New(":0", deps)
	jwt, _ := registerUser(t, srv)
	_, keyID := createAPIKey(t, srv, jwt, "to be revoked")

	rec := doAuthedJSON(t, srv, http.MethodDelete, "/api/v1/apikeys/"+keyID, "", jwt)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	rec = doAuthedJSON(t, srv, http.MethodGet, "/api/v1/apikeys", "", jwt)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var keys []struct {
		ID        string  `json:"id"`
		RevokedAt *string `json:"revoked_at"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&keys); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(keys) != 1 || keys[0].ID != keyID || keys[0].RevokedAt == nil {
		t.Fatalf("expected the revoked key to remain listed with revoked_at set, got %+v", keys)
	}
}
