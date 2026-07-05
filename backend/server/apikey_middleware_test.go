package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sukhera/ping/store"
)

type fakeAPIKeyAuthenticator struct {
	authenticateFn func(ctx context.Context, plainKey string) (store.AuthenticatedAPIKey, error)
	allowFn        func(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error)
}

func (f *fakeAPIKeyAuthenticator) AuthenticateAPIKey(ctx context.Context, plainKey string) (store.AuthenticatedAPIKey, error) {
	return f.authenticateFn(ctx, plainKey)
}

func (f *fakeAPIKeyAuthenticator) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	if f.allowFn == nil {
		return true, 0, nil
	}
	return f.allowFn(ctx, key, limit, window)
}

func TestRequireAuthOrAPIKey_ValidJWTSetsUserID(t *testing.T) {
	deps := testDeps(t)
	token, err := issueAccessToken(deps.JWTPrivateKey, "user-jwt", 15*time.Minute)
	if err != nil {
		t.Fatalf("issueAccessToken: %v", err)
	}
	keys := &fakeAPIKeyAuthenticator{
		authenticateFn: func(ctx context.Context, plainKey string) (store.AuthenticatedAPIKey, error) {
			t.Fatal("AuthenticateAPIKey should not be called for a JWT")
			return store.AuthenticatedAPIKey{}, nil
		},
	}

	var gotUserID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserID = userIDFromContext(r.Context())
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/monitors", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	requireAuthOrAPIKey(deps.JWTPublicKey, keys)(next).ServeHTTP(rec, req)

	if gotUserID != "user-jwt" {
		t.Errorf("userIDFromContext = %q, want user-jwt", gotUserID)
	}
}

func TestRequireAuthOrAPIKey_ValidAPIKeySetsUserID(t *testing.T) {
	deps := testDeps(t)
	keys := &fakeAPIKeyAuthenticator{
		authenticateFn: func(ctx context.Context, plainKey string) (store.AuthenticatedAPIKey, error) {
			if plainKey != "pk_validtoken" {
				t.Fatalf("plainKey = %q, want pk_validtoken", plainKey)
			}
			return store.AuthenticatedAPIKey{UserID: "user-key", KeyID: "key-1"}, nil
		},
	}

	var gotUserID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserID = userIDFromContext(r.Context())
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/monitors", nil)
	req.Header.Set("Authorization", "Bearer pk_validtoken")
	rec := httptest.NewRecorder()
	requireAuthOrAPIKey(deps.JWTPublicKey, keys)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if gotUserID != "user-key" {
		t.Errorf("userIDFromContext = %q, want user-key", gotUserID)
	}
}

func TestRequireAuthOrAPIKey_RevokedOrUnknownKeyReturns401(t *testing.T) {
	deps := testDeps(t)
	keys := &fakeAPIKeyAuthenticator{
		authenticateFn: func(ctx context.Context, plainKey string) (store.AuthenticatedAPIKey, error) {
			return store.AuthenticatedAPIKey{}, testStoreError{msg: "invalid or revoked api key", status: http.StatusUnauthorized}
		},
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called for a revoked key")
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/monitors", nil)
	req.Header.Set("Authorization", "Bearer pk_revoked")
	rec := httptest.NewRecorder()
	requireAuthOrAPIKey(deps.JWTPublicKey, keys)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestRequireAuthOrAPIKey_RateLimitedKeyReturns429(t *testing.T) {
	deps := testDeps(t)
	keys := &fakeAPIKeyAuthenticator{
		authenticateFn: func(ctx context.Context, plainKey string) (store.AuthenticatedAPIKey, error) {
			return store.AuthenticatedAPIKey{UserID: "user-key", KeyID: "key-1"}, nil
		},
		allowFn: func(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
			return false, 30 * time.Second, nil
		},
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called when rate limited")
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/monitors", nil)
	req.Header.Set("Authorization", "Bearer pk_toomany")
	rec := httptest.NewRecorder()
	requireAuthOrAPIKey(deps.JWTPublicKey, keys)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
}

func TestRequireAuthOrAPIKey_MissingHeaderReturns401(t *testing.T) {
	deps := testDeps(t)
	keys := &fakeAPIKeyAuthenticator{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/monitors", nil)
	rec := httptest.NewRecorder()
	requireAuthOrAPIKey(deps.JWTPublicKey, keys)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
