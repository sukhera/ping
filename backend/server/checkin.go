package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sukhera/ping/store"
)

const (
	// maxCheckinBody caps how much of a ping body is stored. Bodies are
	// truncated at this size, never rejected — a ping must always succeed
	// (PRD F1.3). 10 KB matches the ticket and Healthchecks' default.
	maxCheckinBody = 10 << 10 // 10 KiB

	// Ping rate limit: generous per-IP, since a single host may legitimately
	// ping many monitors. Fails open like the auth limiter (Store.Allow).
	pingRateLimit  = 120
	pingRateWindow = time.Minute
)

// checkinStore is the subset of *store.Store the ingest handler needs, kept as
// an interface so handler tests can inject a fake without touching Postgres.
type checkinStore interface {
	RecordCheckin(ctx context.Context, p store.RecordCheckinParams) (store.RecordCheckinResult, error)
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error)
}

type checkinHandler struct {
	store checkinStore
	deps  Deps
}

func newCheckinHandler(s checkinStore, deps Deps) *checkinHandler {
	return &checkinHandler{store: s, deps: deps}
}

// success handles GET/POST/HEAD /p/{slug} — a successful check-in.
func (h *checkinHandler) success(w http.ResponseWriter, r *http.Request) {
	h.ingest(w, r, store.CheckinSuccess)
}

// start handles /p/{slug}/start — a "job started" signal (checkin only).
func (h *checkinHandler) start(w http.ResponseWriter, r *http.Request) {
	h.ingest(w, r, store.CheckinStart)
}

// fail handles /p/{slug}/fail — an explicit failure → immediate down.
func (h *checkinHandler) fail(w http.ResponseWriter, r *http.Request) {
	h.ingest(w, r, store.CheckinFail)
}

// exitCode handles /p/{slug}/{code} — a numeric exit code: 0 is success, any
// non-zero value is a failure. A non-numeric segment is treated as success (a
// ping must never be rejected); it simply won't match the /start or /fail
// routes, which are registered ahead of this catch-all.
func (h *checkinHandler) exitCode(w http.ResponseWriter, r *http.Request) {
	kind := store.CheckinSuccess
	if code, err := strconv.Atoi(chi.URLParam(r, "code")); err == nil && code != 0 {
		kind = store.CheckinFail
	}
	h.ingest(w, r, kind)
}

// ingest is the shared fast path: rate-limit, record the check-in, and always
// answer a tiny 200. It never rejects a well-formed ping and never dispatches
// alerts (that is the alerter worker, PING-012).
func (h *checkinHandler) ingest(w http.ResponseWriter, r *http.Request, kind store.CheckinKind) {
	ctx := r.Context()
	ip := clientIP(r)

	allowed, retryAfter, err := h.store.Allow(ctx, "rate:ping:"+ip, pingRateLimit, pingRateWindow)
	if err != nil {
		slog.WarnContext(ctx, "ping rate limit check failed, allowing request",
			"request_id", requestIDFromContext(ctx), "error", err)
	}
	if !allowed {
		writeRateLimited(w, retryAfter)
		return
	}

	slug := chi.URLParam(r, "slug")

	// HEAD carries no body; other methods have their body read and truncated.
	body := ""
	if r.Method != http.MethodHead {
		body = readTruncatedBody(r)
	}

	_, err = h.store.RecordCheckin(ctx, store.RecordCheckinParams{
		Slug:      slug,
		Kind:      kind,
		SourceIP:  ip,
		UserAgent: r.UserAgent(),
		Body:      body,
		Now:       time.Now(),
	})
	switch {
	case err == nil:
		// recorded
	case errors.Is(err, store.ErrNotFound):
		// Unknown slug: answer 200 with nothing recorded (anti-enumeration,
		// matching Healthchecks). See docs/API.md.
	default:
		// Unexpected failure: still answer 200 so a client cron never
		// retry-storms, but log it for operators.
		slog.ErrorContext(ctx, "record checkin failed",
			"request_id", requestIDFromContext(ctx), "slug", slug, "error", err)
	}

	writeCheckinOK(w, r)
}

// readTruncatedBody reads at most maxCheckinBody bytes of the request body,
// discarding the rest. It never errors out of the request: a read failure
// yields whatever was read so far.
func readTruncatedBody(r *http.Request) string {
	buf, _ := io.ReadAll(io.LimitReader(r.Body, maxCheckinBody))
	// Drain any remainder so keep-alive connections stay usable; ignore errors.
	_, _ = io.Copy(io.Discard, r.Body)
	return string(buf)
}

// writeCheckinOK writes the tiny success response. HEAD gets headers only.
func writeCheckinOK(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte("OK"))
	}
}
