package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sukhera/ping/alert"
)

type fakeAlertingStore struct {
	email string
	err   error
}

func (f *fakeAlertingStore) UserEmailByID(context.Context, string) (string, error) {
	return f.email, f.err
}

type fakeChannel struct {
	gotTo string
	err   error
}

func (f *fakeChannel) Send(_ context.Context, msg alert.Message) error {
	f.gotTo = msg.To
	return f.err
}

func newAlertingReq(userID string) *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/alerting/test", nil)
	return withAuthedUser(req, userID)
}

func TestSendTest_DeliversToCallerEmail(t *testing.T) {
	fs := &fakeAlertingStore{email: "me@example.com"}
	ch := &fakeChannel{}
	deps := testDeps(t)
	deps.AlertChannel = ch
	h := newAlertingHandler(fs, deps)

	rec := httptest.NewRecorder()
	h.sendTest(rec, newAlertingReq("user-1"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ch.gotTo != "me@example.com" {
		t.Errorf("sent to %q, want the caller's own email", ch.gotTo)
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["delivered_to"] != "me@example.com" {
		t.Errorf("delivered_to = %q", resp["delivered_to"])
	}
}

func TestSendTest_NoChannelReports503(t *testing.T) {
	h := newAlertingHandler(&fakeAlertingStore{email: "me@example.com"}, testDeps(t))

	rec := httptest.NewRecorder()
	h.sendTest(rec, newAlertingReq("user-1"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestSendTest_ErrorClassificationToStatus(t *testing.T) {
	tests := []struct {
		name       string
		sendErr    error
		wantStatus int
	}{
		{"permanent -> 502", &alert.SendError{Op: "rcpt", Retryable: false, Err: errors.New("550 rejected")}, http.StatusBadGateway},
		{"retryable -> 503", &alert.SendError{Op: "dial", Retryable: true, Err: errors.New("timeout")}, http.StatusServiceUnavailable},
		{"not configured -> 503", alert.ErrNotConfigured, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := testDeps(t)
			deps.AlertChannel = &fakeChannel{err: tt.sendErr}
			h := newAlertingHandler(&fakeAlertingStore{email: "me@example.com"}, deps)

			rec := httptest.NewRecorder()
			h.sendTest(rec, newAlertingReq("user-1"))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			// The safe client message must never contain SMTP internals.
			if body := rec.Body.String(); strings.Contains(body, "550") || strings.Contains(body, "timeout") {
				t.Errorf("client message leaked internal error detail: %s", body)
			}
		})
	}
}
