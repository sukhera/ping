package server

import (
	"context"
	"crypto/rsa"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/sukhera/ping/store"
)

const bearerPrefix = "Bearer "

// apiKeyRateLimit bounds requests authenticated by a single API key,
// independent of the per-IP limits on auth/ping endpoints — a key is a
// distinct credential that can be scripted far harder than a browser login.
const (
	apiKeyRateLimit  = 300
	apiKeyRateWindow = time.Minute
)

// apiKeyAuthenticator is the subset of *store.Store requireAuth needs to
// validate a "pk_..." bearer token, kept narrow so middleware tests can
// inject a fake.
type apiKeyAuthenticator interface {
	AuthenticateAPIKey(ctx context.Context, plainKey string) (store.AuthenticatedAPIKey, error)
	Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
}

// requireAuth validates the request's JWT access token and stashes the
// authenticated user ID in context via withUserID. Handlers wrapped by this
// middleware can assume userIDFromContext(r.Context()) is populated —
// AuthMiddleware runs on every protected route per the security-specialist
// skill, not left to client-side hiding.
//
// This is JWT-only: routes that manage API keys themselves (create/list/
// revoke) intentionally require a logged-in web session, not a key, so a
// leaked key can't mint or revoke other keys for the account.
func requireAuth(pub *rsa.PublicKey) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing or malformed authorization header"})
				return
			}

			claims, err := parseAccessToken(pub, token)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid or expired access token"})
				return
			}

			ctx := withUserID(r.Context(), claims.Subject)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// requireAuthOrAPIKey accepts either a JWT access token or a "pk_..." API
// key, so the management API (PING-016) is fully usable from a script with
// just a key, per its AC. A revoked or unknown key is rejected on every
// request (GetAPIKeyByHash filters revoked_at IS NULL), so there is no cache
// window after revocation. Each authenticated key is separately rate-limited
// so one leaked/misbehaving key can't exhaust another's budget.
func requireAuthOrAPIKey(pub *rsa.PublicKey, keys apiKeyAuthenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing or malformed authorization header"})
				return
			}

			if strings.HasPrefix(token, "pk_") {
				authed, err := keys.AuthenticateAPIKey(r.Context(), token)
				if err != nil {
					writeError(w, r, err)
					return
				}

				allowed, retryAfter, err := keys.Allow(r.Context(), "rate:apikey:"+authed.KeyID, apiKeyRateLimit, apiKeyRateWindow)
				if err != nil {
					// Allow fails open (see store.Allow) — a Redis outage must not
					// lock every API key user out of the management API.
					slog.WarnContext(r.Context(), "api key rate limit check failed, allowing request",
						"request_id", requestIDFromContext(r.Context()), "error", err)
				}
				if !allowed {
					writeRateLimited(w, retryAfter)
					return
				}

				ctx := withUserID(r.Context(), authed.UserID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			claims, err := parseAccessToken(pub, token)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid or expired access token"})
				return
			}

			ctx := withUserID(r.Context(), claims.Subject)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerToken(r *http.Request) (string, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), bearerPrefix)
	if !ok || token == "" {
		return "", false
	}
	return token, true
}
