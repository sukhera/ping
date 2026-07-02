package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequireAuth_MissingHeaderReturns401(t *testing.T) {
	deps := testDeps(t)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/monitors", nil)
	rec := httptest.NewRecorder()
	requireAuth(deps.JWTPublicKey)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Error("next handler should not be called on missing auth header")
	}
}

func TestRequireAuth_MalformedHeaderReturns401(t *testing.T) {
	deps := testDeps(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/monitors", nil)
	req.Header.Set("Authorization", "not-bearer-token")
	rec := httptest.NewRecorder()
	requireAuth(deps.JWTPublicKey)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestRequireAuth_InvalidTokenReturns401(t *testing.T) {
	deps := testDeps(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/monitors", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-jwt")
	rec := httptest.NewRecorder()
	requireAuth(deps.JWTPublicKey)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestRequireAuth_ValidTokenSetsUserIDInContext(t *testing.T) {
	deps := testDeps(t)
	token, err := issueAccessToken(deps.JWTPrivateKey, "user-42", 15*time.Minute)
	if err != nil {
		t.Fatalf("issueAccessToken: %v", err)
	}

	var gotUserID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserID = userIDFromContext(r.Context())
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/monitors", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	requireAuth(deps.JWTPublicKey)(next).ServeHTTP(rec, req)

	if gotUserID != "user-42" {
		t.Errorf("userIDFromContext = %q, want user-42", gotUserID)
	}
}
