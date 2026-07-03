package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/sukhera/ping/alert"
)

// sendTestTimeout bounds an SMTP round-trip for the test endpoint so a wedged
// mail server can't hold the request open.
const sendTestTimeout = 20 * time.Second

// alertingStore is the store subset the alerting handlers need.
type alertingStore interface {
	UserEmailByID(ctx context.Context, userID string) (string, error)
}

type alertingHandler struct {
	store alertingStore
	deps  Deps
}

func newAlertingHandler(s alertingStore, deps Deps) *alertingHandler {
	return &alertingHandler{store: s, deps: deps}
}

// sendTest delivers a verification email to the authenticated user's own
// account address (PRD F3, DESIGN §7.5 "Send test email"). It reports SMTP
// failures back to the caller usefully, distinguishing "not configured" from a
// transient delivery error, without leaking credentials.
func (h *alertingHandler) sendTest(w http.ResponseWriter, r *http.Request) {
	if h.deps.AlertChannel == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{
			Error: "email delivery is not configured (set SMTP_HOST and SMTP_FROM)",
		})
		return
	}

	userID := userIDFromContext(r.Context())
	to, err := h.store.UserEmailByID(r.Context(), userID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	msg := alert.Render(alert.Notification{
		Kind: alert.KindTest,
		At:   time.Now(),
	})
	msg.To = to

	ctx, cancel := context.WithTimeout(r.Context(), sendTestTimeout)
	defer cancel()

	if err := h.deps.AlertChannel.Send(ctx, msg); err != nil {
		h.writeSendError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"delivered_to": to})
}

// writeSendError maps a delivery failure to a client response. The underlying
// error is logged (never the SMTP password — SendError carries none) and only
// a safe, actionable message reaches the caller.
func (h *alertingHandler) writeSendError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, alert.ErrNotConfigured) {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{
			Error: "email delivery is not configured (set SMTP_HOST and SMTP_FROM)",
		})
		return
	}

	retryable := alert.IsRetryable(err)
	slog.WarnContext(r.Context(), "test email delivery failed",
		"request_id", requestIDFromContext(r.Context()),
		"retryable", retryable,
		"error", err,
	)

	status := http.StatusBadGateway
	message := "could not deliver test email: the mail server rejected it (check SMTP settings)"
	if retryable {
		status = http.StatusServiceUnavailable
		message = "could not deliver test email: the mail server was temporarily unavailable, try again"
	}
	writeJSON(w, status, errorResponse{Error: message})
}
