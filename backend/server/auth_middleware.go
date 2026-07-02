package server

import (
	"crypto/rsa"
	"net/http"
	"strings"
)

const bearerPrefix = "Bearer "

// requireAuth validates the request's access token and stashes the
// authenticated user ID in context via withUserID. Handlers wrapped by this
// middleware can assume userIDFromContext(r.Context()) is populated —
// AuthMiddleware runs on every protected route per the security-specialist
// skill, not left to client-side hiding.
func requireAuth(pub *rsa.PublicKey) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			token, ok := strings.CutPrefix(header, bearerPrefix)
			if !ok || token == "" {
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
