package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sukhera/ping/store"
)

type fakeAuthStore struct {
	registerFn            func(ctx context.Context, email, password string, registrationOpen bool) (store.AuthUser, error)
	authenticateFn        func(ctx context.Context, email, password string) (store.AuthUser, error)
	issueRefreshTokenFn   func(ctx context.Context, userID string, ttl time.Duration) (store.RefreshToken, error)
	rotateRefreshTokenFn  func(ctx context.Context, plainToken string, ttl time.Duration) (store.RefreshToken, string, error)
	revokeFamilyByTokenFn func(ctx context.Context, plainToken string) error
	allowFn               func(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error)
}

func (f *fakeAuthStore) Register(ctx context.Context, email, password string, registrationOpen bool) (store.AuthUser, error) {
	return f.registerFn(ctx, email, password, registrationOpen)
}

func (f *fakeAuthStore) Authenticate(ctx context.Context, email, password string) (store.AuthUser, error) {
	return f.authenticateFn(ctx, email, password)
}

func (f *fakeAuthStore) IssueRefreshToken(ctx context.Context, userID string, ttl time.Duration) (store.RefreshToken, error) {
	return f.issueRefreshTokenFn(ctx, userID, ttl)
}

func (f *fakeAuthStore) RotateRefreshToken(ctx context.Context, plainToken string, ttl time.Duration) (store.RefreshToken, string, error) {
	return f.rotateRefreshTokenFn(ctx, plainToken, ttl)
}

func (f *fakeAuthStore) RevokeFamilyByToken(ctx context.Context, plainToken string) error {
	return f.revokeFamilyByTokenFn(ctx, plainToken)
}

func (f *fakeAuthStore) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	if f.allowFn == nil {
		return true, 0, nil
	}
	return f.allowFn(ctx, key, limit, window)
}

func testDeps(t *testing.T) Deps {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return Deps{
		JWTPrivateKey:    priv,
		JWTPublicKey:     &priv.PublicKey,
		JWTAccessTTL:     15 * time.Minute,
		JWTRefreshTTL:    720 * time.Hour,
		RegistrationOpen: true,
		CookieSecure:     false,
	}
}

func TestRegister_MalformedJSONReturns400(t *testing.T) {
	h := newAuthHandler(&fakeAuthStore{}, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/auth/register", strings.NewReader("{not json"))
	rec := httptest.NewRecorder()
	h.register(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestRegister_ShortPasswordReturns422(t *testing.T) {
	h := newAuthHandler(&fakeAuthStore{}, testDeps(t))

	body := `{"email":"a@example.com","password":"short"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.register(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func TestRegister_InvalidEmailReturns422(t *testing.T) {
	h := newAuthHandler(&fakeAuthStore{}, testDeps(t))

	body := `{"email":"not-an-email","password":"correcthorsebatterystaple"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.register(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func TestRegister_SuccessSetsCookieAndReturns201(t *testing.T) {
	fs := &fakeAuthStore{
		registerFn: func(ctx context.Context, email, password string, registrationOpen bool) (store.AuthUser, error) {
			return store.AuthUser{ID: "user-1", Email: email}, nil
		},
		issueRefreshTokenFn: func(ctx context.Context, userID string, ttl time.Duration) (store.RefreshToken, error) {
			return store.RefreshToken{PlainToken: "plain-token", ExpiresAt: time.Now().Add(ttl)}, nil
		},
	}
	h := newAuthHandler(fs, testDeps(t))

	body := `{"email":"a@example.com","password":"correcthorsebatterystaple"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.register(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != refreshCookieName {
		t.Errorf("cookie name = %q, want %q", c.Name, refreshCookieName)
	}
	if c.Value != "plain-token" {
		t.Errorf("cookie value = %q, want plain-token", c.Value)
	}
	if c.Path != refreshCookiePath {
		t.Errorf("cookie path = %q, want %q", c.Path, refreshCookiePath)
	}
	if !c.HttpOnly {
		t.Error("expected HttpOnly cookie")
	}
	if c.Secure {
		t.Error("expected Secure=false in test deps (dev mode)")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}

	var resp authResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AccessToken == "" {
		t.Error("expected non-empty access_token")
	}
	if resp.User.ID != "user-1" {
		t.Errorf("user.id = %q, want user-1", resp.User.ID)
	}
}

func TestLogin_SuccessReturns200(t *testing.T) {
	fs := &fakeAuthStore{
		authenticateFn: func(ctx context.Context, email, password string) (store.AuthUser, error) {
			return store.AuthUser{ID: "user-1", Email: email}, nil
		},
		issueRefreshTokenFn: func(ctx context.Context, userID string, ttl time.Duration) (store.RefreshToken, error) {
			return store.RefreshToken{PlainToken: "plain-token", ExpiresAt: time.Now().Add(ttl)}, nil
		},
	}
	h := newAuthHandler(fs, testDeps(t))

	body := `{"email":"a@example.com","password":"correcthorsebatterystaple"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.login(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestRateLimit_BlocksAndSetsRetryAfter(t *testing.T) {
	fs := &fakeAuthStore{
		allowFn: func(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
			return false, 42 * time.Second, nil
		},
	}
	h := newAuthHandler(fs, testDeps(t))

	body := `{"email":"a@example.com","password":"correcthorsebatterystaple"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.login(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "42" {
		t.Errorf("Retry-After = %q, want 42", got)
	}
}

func TestRateLimit_DisabledBypassesLimiterEntirely(t *testing.T) {
	// e2e-only escape hatch (Deps.AuthRateLimitDisabled, PING_ENV=test): the
	// limiter must not even be consulted, and a would-be-blocking verdict must
	// not stop the request.
	allowCalled := false
	fs := &fakeAuthStore{
		allowFn: func(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
			allowCalled = true
			return false, time.Minute, nil // would block if consulted
		},
		authenticateFn: func(ctx context.Context, email, password string) (store.AuthUser, error) {
			return store.AuthUser{ID: "user-1", Email: email}, nil
		},
		issueRefreshTokenFn: func(ctx context.Context, userID string, ttl time.Duration) (store.RefreshToken, error) {
			return store.RefreshToken{PlainToken: "plain-token", ExpiresAt: time.Now().Add(ttl)}, nil
		},
	}
	deps := testDeps(t)
	deps.AuthRateLimitDisabled = true
	h := newAuthHandler(fs, deps)

	body := `{"email":"a@example.com","password":"correcthorsebatterystaple"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.login(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (limiter disabled)", rec.Code)
	}
	if allowCalled {
		t.Error("Allow was consulted, want limiter bypassed entirely when disabled")
	}
}

func TestRateLimit_FailsOpenOnRedisError(t *testing.T) {
	fs := &fakeAuthStore{
		allowFn: func(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
			return true, 0, context.DeadlineExceeded
		},
		authenticateFn: func(ctx context.Context, email, password string) (store.AuthUser, error) {
			return store.AuthUser{ID: "user-1", Email: email}, nil
		},
		issueRefreshTokenFn: func(ctx context.Context, userID string, ttl time.Duration) (store.RefreshToken, error) {
			return store.RefreshToken{PlainToken: "plain-token", ExpiresAt: time.Now().Add(ttl)}, nil
		},
	}
	h := newAuthHandler(fs, testDeps(t))

	body := `{"email":"a@example.com","password":"correcthorsebatterystaple"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.login(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (fail-open on rate limiter error)", rec.Code)
	}
}

func TestLogout_ClearsCookieRegardlessOfCookiePresence(t *testing.T) {
	h := newAuthHandler(&fakeAuthStore{}, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/auth/logout", nil)
	rec := httptest.NewRecorder()
	h.logout(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Fatalf("expected a cleared cookie (MaxAge < 0), got %+v", cookies)
	}
}

func TestRefresh_MissingCookieReturns401(t *testing.T) {
	h := newAuthHandler(&fakeAuthStore{}, testDeps(t))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/auth/refresh", nil)
	rec := httptest.NewRecorder()
	h.refresh(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
