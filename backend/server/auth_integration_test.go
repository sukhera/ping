//go:build integration

package server_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sukhera/ping/server"
)

func testAuthDeps(t *testing.T, registrationOpen bool) server.Deps {
	t.Helper()

	deps, _ := testDeps(t)

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}

	deps.JWTPrivateKey = priv
	deps.JWTPublicKey = &priv.PublicKey
	deps.JWTAccessTTL = 15 * time.Minute
	deps.JWTRefreshTTL = 720 * time.Hour
	deps.RegistrationOpen = registrationOpen
	deps.CookieSecure = false

	return deps
}

func uniqueEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%d@example.com", t.Name(), time.Now().UnixNano())
}

// testRemoteAddr derives a stable-per-test, unique-across-tests RemoteAddr so
// each test's auth rate-limit bucket (keyed by IP in Redis) is independent —
// otherwise tests sharing httptest's default RemoteAddr would trip each
// other's rate limits when run in the same process against real Redis.
func testRemoteAddr(t *testing.T) string {
	t.Helper()
	sum := 0
	for _, c := range t.Name() {
		sum = sum*31 + int(c)
	}
	if sum < 0 {
		sum = -sum
	}
	return fmt.Sprintf("10.%d.%d.%d:12345", (sum>>16)%256, (sum>>8)%256, sum%256)
}

func doJSON(t *testing.T, srv *http.Server, method, path, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequestWithContext(context.Background(), method, path, nil)
	}
	req.RemoteAddr = testRemoteAddr(t)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

func TestAuth_FullFlow(t *testing.T) {
	deps := testAuthDeps(t, true)
	srv := server.New(":0", deps)
	email := uniqueEmail(t)

	registerBody := fmt.Sprintf(`{"email":%q,"password":"correcthorsebatterystaple"}`, email)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/auth/register", registerBody, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie after register, got %d", len(cookies))
	}
	var registerResp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&registerResp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	if registerResp["access_token"] == "" || registerResp["access_token"] == nil {
		t.Fatal("expected non-empty access_token from register")
	}

	loginBody := fmt.Sprintf(`{"email":%q,"password":"correcthorsebatterystaple"}`, email)
	rec = doJSON(t, srv, http.MethodPost, "/api/v1/auth/login", loginBody, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	loginCookies := rec.Result().Cookies()
	if len(loginCookies) != 1 {
		t.Fatalf("expected 1 cookie after login, got %d", len(loginCookies))
	}

	rec = doJSON(t, srv, http.MethodPost, "/api/v1/auth/refresh", "", loginCookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	refreshCookies := rec.Result().Cookies()
	if len(refreshCookies) != 1 {
		t.Fatalf("expected 1 cookie after refresh, got %d", len(refreshCookies))
	}
	if refreshCookies[0].Value == loginCookies[0].Value {
		t.Fatal("expected refresh to rotate the cookie value")
	}

	rec = doJSON(t, srv, http.MethodPost, "/api/v1/auth/logout", "", refreshCookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, srv, http.MethodPost, "/api/v1/auth/refresh", "", refreshCookies)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("post-logout refresh status = %d, want 401", rec.Code)
	}
}

func TestAuth_RegisterClosed_Returns403(t *testing.T) {
	deps := testAuthDeps(t, false)
	srv := server.New(":0", deps)

	body := fmt.Sprintf(`{"email":%q,"password":"correcthorsebatterystaple"}`, uniqueEmail(t))
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/auth/register", body, nil)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "registration is closed" {
		t.Errorf("error = %q, want %q", resp["error"], "registration is closed")
	}
}

func TestAuth_RegisterDuplicateEmail_Returns409(t *testing.T) {
	deps := testAuthDeps(t, true)
	srv := server.New(":0", deps)
	email := uniqueEmail(t)
	body := fmt.Sprintf(`{"email":%q,"password":"correcthorsebatterystaple"}`, email)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/auth/register", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first register status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, srv, http.MethodPost, "/api/v1/auth/register", body, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second register status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

func TestAuth_LoginWrongPassword_Returns401(t *testing.T) {
	deps := testAuthDeps(t, true)
	srv := server.New(":0", deps)
	email := uniqueEmail(t)
	registerBody := fmt.Sprintf(`{"email":%q,"password":"correcthorsebatterystaple"}`, email)
	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/auth/register", registerBody, nil); rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201", rec.Code)
	}

	loginBody := fmt.Sprintf(`{"email":%q,"password":"wrong-password-here"}`, email)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/auth/login", loginBody, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

// TestAuth_RefreshTokenReuseRevokesFamily is the acceptance-criterion test:
// replaying a rotated refresh token must revoke the entire family, so even
// the legitimate next token in that lineage stops working.
func TestAuth_RefreshTokenReuseRevokesFamily(t *testing.T) {
	deps := testAuthDeps(t, true)
	srv := server.New(":0", deps)
	email := uniqueEmail(t)
	registerBody := fmt.Sprintf(`{"email":%q,"password":"correcthorsebatterystaple"}`, email)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/auth/register", registerBody, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201", rec.Code)
	}
	tokenA := rec.Result().Cookies()
	if len(tokenA) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(tokenA))
	}

	rec = doJSON(t, srv, http.MethodPost, "/api/v1/auth/refresh", "", tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("first refresh status = %d, want 200", rec.Code)
	}
	tokenB := rec.Result().Cookies()
	if len(tokenB) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(tokenB))
	}

	// Replay A (already rotated) — must be rejected.
	rec = doJSON(t, srv, http.MethodPost, "/api/v1/auth/refresh", "", tokenA)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("replaying rotated token A status = %d, want 401", rec.Code)
	}

	// B was the legitimate next token, but the family must now be fully
	// revoked because A was reused — B must also be rejected.
	rec = doJSON(t, srv, http.MethodPost, "/api/v1/auth/refresh", "", tokenB)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("using legitimate token B after family revocation status = %d, want 401 (family must be fully revoked)", rec.Code)
	}
}

func TestAuth_Logout_ClearsCookieAndRevokesFamily(t *testing.T) {
	deps := testAuthDeps(t, true)
	srv := server.New(":0", deps)
	email := uniqueEmail(t)
	registerBody := fmt.Sprintf(`{"email":%q,"password":"correcthorsebatterystaple"}`, email)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/auth/register", registerBody, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201", rec.Code)
	}
	cookies := rec.Result().Cookies()

	rec = doJSON(t, srv, http.MethodPost, "/api/v1/auth/logout", "", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", rec.Code)
	}
	cleared := rec.Result().Cookies()
	if len(cleared) != 1 || cleared[0].MaxAge >= 0 {
		t.Fatalf("expected logout to clear the cookie, got %+v", cleared)
	}

	rec = doJSON(t, srv, http.MethodPost, "/api/v1/auth/refresh", "", cookies)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("post-logout refresh status = %d, want 401", rec.Code)
	}
}

func TestAuth_LoginRateLimit_SixthAttempt429(t *testing.T) {
	deps := testAuthDeps(t, true)
	srv := server.New(":0", deps)

	body := fmt.Sprintf(`{"email":%q,"password":"whatever-wrong-pw"}`, uniqueEmail(t))

	var last *httptest.ResponseRecorder
	for i := 0; i < 6; i++ {
		last = doJSON(t, srv, http.MethodPost, "/api/v1/auth/login", body, nil)
	}

	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("6th attempt status = %d, want 429; body = %s", last.Code, last.Body.String())
	}
	retryAfter := last.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("expected Retry-After header on 429 response")
	}
}
