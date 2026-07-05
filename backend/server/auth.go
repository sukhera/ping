package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"strconv"
	"time"

	"github.com/sukhera/ping/store"
)

const (
	refreshCookieName = "ping_refresh"
	refreshCookiePath = "/api/v1/auth"

	authRateLimit  = 5
	authRateWindow = time.Minute

	minPasswordLen = 12

	// maxAuthBodyBytes bounds the auth request body size read before
	// decoding, so a large payload can't force excessive buffering ahead of
	// the (tight) auth rate limiter kicking in.
	maxAuthBodyBytes = 4 << 10 // 4 KiB — email+password JSON is a few hundred bytes at most
)

// authStore is the subset of *store.Store the auth handlers need, kept as an
// interface so handler tests can inject a fake without touching Postgres/Redis.
type authStore interface {
	Register(ctx context.Context, email, password string, registrationOpen bool) (store.AuthUser, error)
	Authenticate(ctx context.Context, email, password string) (store.AuthUser, error)
	IssueRefreshToken(ctx context.Context, userID string, ttl time.Duration) (store.RefreshToken, error)
	RotateRefreshToken(ctx context.Context, plainToken string, ttl time.Duration) (store.RefreshToken, string, error)
	RevokeFamilyByToken(ctx context.Context, plainToken string) error
	Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
}

type authHandler struct {
	store authStore
	deps  Deps
}

func newAuthHandler(s authStore, deps Deps) *authHandler {
	return &authHandler{store: s, deps: deps}
}

type emailPasswordRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	AccessToken string `json:"access_token"`
	User        struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"user"`
}

func (h *authHandler) register(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if !h.checkRateLimit(w, r, "rate:register:"+clientIP(r)) {
		return
	}

	var req emailPasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !validEmailPassword(w, req) {
		return
	}

	user, err := h.store.Register(ctx, req.Email, req.Password, h.deps.RegistrationOpen)
	if err != nil {
		writeError(w, r, err)
		return
	}

	h.issueTokenPair(w, r, http.StatusCreated, user.ID, user.Email)
}

func (h *authHandler) login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if !h.checkRateLimit(w, r, "rate:login:"+clientIP(r)) {
		return
	}

	var req emailPasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	user, err := h.store.Authenticate(ctx, req.Email, req.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}

	h.issueTokenPair(w, r, http.StatusOK, user.ID, user.Email)
}

func (h *authHandler) refresh(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cookie, err := r.Cookie(refreshCookieName)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing refresh token"})
		return
	}

	rt, userID, err := h.store.RotateRefreshToken(ctx, cookie.Value, h.deps.JWTRefreshTTL)
	if err != nil {
		clearRefreshCookie(w, h.deps.CookieSecure)
		writeError(w, r, err)
		return
	}

	access, err := issueAccessToken(h.deps.JWTPrivateKey, userID, h.deps.JWTAccessTTL)
	if err != nil {
		writeError(w, r, err)
		return
	}

	setRefreshCookie(w, rt.PlainToken, rt.ExpiresAt, h.deps.CookieSecure)
	resp := authResponse{AccessToken: access}
	resp.User.ID = userID
	writeJSON(w, http.StatusOK, resp)
}

func (h *authHandler) logout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cookie, err := r.Cookie(refreshCookieName)
	if err == nil {
		if revokeErr := h.store.RevokeFamilyByToken(ctx, cookie.Value); revokeErr != nil {
			slog.ErrorContext(ctx, "logout: revoke family failed",
				"request_id", requestIDFromContext(ctx), "error", revokeErr)
		}
	}
	clearRefreshCookie(w, h.deps.CookieSecure)
	writeJSON(w, http.StatusOK, nil)
}

func (h *authHandler) checkRateLimit(w http.ResponseWriter, r *http.Request, key string) bool {
	// e2e-only escape hatch: the Playwright suite shares one IP across workers
	// and would otherwise exhaust the 5/min per-IP budget. Gated on
	// PING_ENV=test (see Deps.AuthRateLimitDisabled) so it can never weaken a
	// dev or production deployment.
	if h.deps.AuthRateLimitDisabled {
		return true
	}

	allowed, retryAfter, err := h.store.Allow(r.Context(), key, authRateLimit, authRateWindow)
	if err != nil {
		slog.WarnContext(r.Context(), "rate limit check failed, allowing request",
			"request_id", requestIDFromContext(r.Context()), "error", err)
	}
	if !allowed {
		writeRateLimited(w, retryAfter)
		return false
	}
	return true
}

func (h *authHandler) issueTokenPair(w http.ResponseWriter, r *http.Request, status int, userID, email string) {
	access, err := issueAccessToken(h.deps.JWTPrivateKey, userID, h.deps.JWTAccessTTL)
	if err != nil {
		writeError(w, r, err)
		return
	}
	rt, err := h.store.IssueRefreshToken(r.Context(), userID, h.deps.JWTRefreshTTL)
	if err != nil {
		writeError(w, r, err)
		return
	}
	setRefreshCookie(w, rt.PlainToken, rt.ExpiresAt, h.deps.CookieSecure)

	resp := authResponse{AccessToken: access}
	resp.User.ID = userID
	resp.User.Email = email
	writeJSON(w, status, resp)
}

// decodeJSON bounds the request body to maxAuthBodyBytes before decoding, so
// an oversized payload can't force excessive buffering ahead of the auth
// rate limiter. Writes a 400 and returns false on any decode failure,
// including "body too large".
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return false
	}
	return true
}

func validEmailPassword(w http.ResponseWriter, req emailPasswordRequest) bool {
	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, errorResponse{Error: "invalid email address"})
		return false
	}
	if len(req.Password) < minPasswordLen {
		writeJSON(w, http.StatusUnprocessableEntity, errorResponse{Error: "password must be at least 12 characters"})
		return false
	}
	return true
}

// Secure is a parameter (not a literal true) so it can be false in local dev
// over plain http and true in production; HttpOnly and SameSite are always
// set regardless. gosec's G124 can't prove a variable is always true, hence
// the nolint.
func setRefreshCookie(w http.ResponseWriter, value string, expiresAt time.Time, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec
		Name:     refreshCookieName,
		Value:    value,
		Path:     refreshCookiePath,
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearRefreshCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec
		Name:     refreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
	writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "too many attempts, try again later"})
}

// clientIP uses the direct connection's remote address. Deployments behind a
// reverse proxy will need trusted X-Forwarded-For handling — deliberately not
// built here since no proxy topology is decided yet, and blindly trusting a
// client-controlled header would be a spoofing vector.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
