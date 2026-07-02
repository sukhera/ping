package config

import (
	"strings"
	"testing"
	"time"
)

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func validEnv() map[string]string {
	return map[string]string{
		"PING_PORT":            "8080",
		"PING_ENV":             "development",
		"PING_BASE_URL":        "http://localhost:8080",
		"DATABASE_URL":         "postgres://ping:ping@localhost:5432/ping?sslmode=disable",
		"REDIS_URL":            "redis://localhost:6379/0",
		"CORS_ALLOWED_ORIGIN":  "http://localhost:3000",
		"JWT_PRIVATE_KEY_PATH": "./keys/jwt_private.pem",
		"JWT_PUBLIC_KEY_PATH":  "./keys/jwt_public.pem",
		"JWT_ACCESS_TTL":       "15m",
		"JWT_REFRESH_TTL":      "720h",
		"REGISTRATION_OPEN":    "true",
	}
}

func TestLoad_Valid(t *testing.T) {
	setEnv(t, validEnv())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.Env != "development" {
		t.Errorf("Env = %q, want development", cfg.Env)
	}
	if cfg.DatabaseURL == "" || cfg.RedisURL == "" {
		t.Error("expected DatabaseURL and RedisURL to be populated")
	}
	if cfg.JWTAccessTTL != 15*time.Minute {
		t.Errorf("JWTAccessTTL = %v, want 15m", cfg.JWTAccessTTL)
	}
	if cfg.JWTRefreshTTL != 720*time.Hour {
		t.Errorf("JWTRefreshTTL = %v, want 720h", cfg.JWTRefreshTTL)
	}
	if !cfg.RegistrationOpen {
		t.Error("RegistrationOpen = false, want true")
	}
	if cfg.JWTPrivateKeyPath == "" || cfg.JWTPublicKeyPath == "" {
		t.Error("expected JWTPrivateKeyPath and JWTPublicKeyPath to be populated")
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	tests := []struct {
		name        string
		unset       string
		wantVarName string
	}{
		{name: "missing PING_PORT", unset: "PING_PORT", wantVarName: "PING_PORT"},
		{name: "missing PING_ENV", unset: "PING_ENV", wantVarName: "PING_ENV"},
		{name: "missing PING_BASE_URL", unset: "PING_BASE_URL", wantVarName: "PING_BASE_URL"},
		{name: "missing DATABASE_URL", unset: "DATABASE_URL", wantVarName: "DATABASE_URL"},
		{name: "missing REDIS_URL", unset: "REDIS_URL", wantVarName: "REDIS_URL"},
		{name: "missing CORS_ALLOWED_ORIGIN", unset: "CORS_ALLOWED_ORIGIN", wantVarName: "CORS_ALLOWED_ORIGIN"},
		{name: "missing JWT_PRIVATE_KEY_PATH", unset: "JWT_PRIVATE_KEY_PATH", wantVarName: "JWT_PRIVATE_KEY_PATH"},
		{name: "missing JWT_PUBLIC_KEY_PATH", unset: "JWT_PUBLIC_KEY_PATH", wantVarName: "JWT_PUBLIC_KEY_PATH"},
		{name: "missing JWT_ACCESS_TTL", unset: "JWT_ACCESS_TTL", wantVarName: "JWT_ACCESS_TTL"},
		{name: "missing JWT_REFRESH_TTL", unset: "JWT_REFRESH_TTL", wantVarName: "JWT_REFRESH_TTL"},
		{name: "missing REGISTRATION_OPEN", unset: "REGISTRATION_OPEN", wantVarName: "REGISTRATION_OPEN"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnv()
			delete(env, tt.unset)
			setEnv(t, env)
			t.Setenv(tt.unset, "")

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() expected error when %s is missing, got nil", tt.unset)
			}
			if !strings.Contains(err.Error(), tt.wantVarName) {
				t.Errorf("Load() error = %q, want it to name %s", err.Error(), tt.wantVarName)
			}
		})
	}
}

func TestLoad_InvalidPort(t *testing.T) {
	env := validEnv()
	env["PING_PORT"] = "not-a-number"
	setEnv(t, env)

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for non-integer PING_PORT, got nil")
	}
	if !strings.Contains(err.Error(), "PING_PORT") {
		t.Errorf("Load() error = %q, want it to name PING_PORT", err.Error())
	}
}

func TestLoad_InvalidDuration(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "invalid JWT_ACCESS_TTL", key: "JWT_ACCESS_TTL"},
		{name: "invalid JWT_REFRESH_TTL", key: "JWT_REFRESH_TTL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnv()
			env[tt.key] = "not-a-duration"
			setEnv(t, env)

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() expected error for non-duration %s, got nil", tt.key)
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Errorf("Load() error = %q, want it to name %s", err.Error(), tt.key)
			}
		})
	}
}

func TestLoad_InvalidRegistrationOpen(t *testing.T) {
	env := validEnv()
	env["REGISTRATION_OPEN"] = "not-a-bool"
	setEnv(t, env)

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for non-boolean REGISTRATION_OPEN, got nil")
	}
	if !strings.Contains(err.Error(), "REGISTRATION_OPEN") {
		t.Errorf("Load() error = %q, want it to name REGISTRATION_OPEN", err.Error())
	}
}
