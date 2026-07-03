// Package alert defines the notification channel abstraction and its
// implementations. A Channel turns a rendered Message into a delivered
// notification; the email implementation (SMTP) is the only one in v1, but the
// interface lets Slack/webhook channels slot in later without schema changes
// (PRD F3.6).
//
// This package is pure delivery: it renders templates and sends messages. It
// does not touch Postgres, claim outbox rows, or schedule retries — that is
// the alerter worker's job (PING-012). Package direction: worker/* imports
// alert; alert imports nothing from server or store.
package alert

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotConfigured is returned by a Channel whose transport is not configured
// (e.g. SMTP with no host). Callers should surface this as a clear
// "email delivery is not set up" message rather than a retryable failure.
var ErrNotConfigured = errors.New("alert: channel not configured")

// Message is a rendered, channel-agnostic notification ready to send. Bodies
// are produced by the template functions in this package.
type Message struct {
	To       string
	Subject  string
	TextBody string
	// HTMLBody is an optional richer variant. Channels that support multipart
	// (email) send both; text-only channels ignore it.
	HTMLBody string
}

// Channel delivers a Message. Implementations must classify failures via
// SendError so the caller can decide between retry and give-up.
type Channel interface {
	// Send delivers msg, honouring ctx cancellation/deadline. A nil return
	// means the message was accepted by the transport. A non-nil error should
	// be a *SendError when the failure is attributable to the transport.
	Send(ctx context.Context, msg Message) error
}

// SendError carries a delivery failure together with whether retrying it later
// could plausibly succeed. Transient/5xx/network failures are retryable;
// authentication and other permanent (4xx-class) failures are not, so the
// worker fails them fast instead of burning retry attempts.
type SendError struct {
	// Retryable is true for transient failures worth retrying with backoff.
	Retryable bool
	// Op names the stage that failed (e.g. "dial", "auth", "send") for logs.
	Op string
	// Err is the underlying cause. It must not contain secrets.
	Err error
}

func (e *SendError) Error() string {
	kind := "permanent"
	if e.Retryable {
		kind = "retryable"
	}
	return fmt.Sprintf("alert: %s send failure (%s): %v", kind, e.Op, e.Err)
}

func (e *SendError) Unwrap() error { return e.Err }

// IsRetryable reports whether err (or an error it wraps) is a retryable
// delivery failure. Non-SendError errors are treated as non-retryable.
func IsRetryable(err error) bool {
	if se, ok := errors.AsType[*SendError](err); ok {
		return se.Retryable
	}
	return false
}
