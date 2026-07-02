package config

import (
	"strings"
	"testing"
)

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func validEnv() map[string]string {
	return map[string]string{
		"PING_PORT":           "8080",
		"PING_ENV":            "development",
		"DATABASE_URL":        "postgres://ping:ping@localhost:5432/ping?sslmode=disable",
		"REDIS_URL":           "redis://localhost:6379/0",
		"CORS_ALLOWED_ORIGIN": "http://localhost:3000",
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
}

func TestLoad_MissingRequired(t *testing.T) {
	tests := []struct {
		name        string
		unset       string
		wantVarName string
	}{
		{name: "missing PING_PORT", unset: "PING_PORT", wantVarName: "PING_PORT"},
		{name: "missing PING_ENV", unset: "PING_ENV", wantVarName: "PING_ENV"},
		{name: "missing DATABASE_URL", unset: "DATABASE_URL", wantVarName: "DATABASE_URL"},
		{name: "missing REDIS_URL", unset: "REDIS_URL", wantVarName: "REDIS_URL"},
		{name: "missing CORS_ALLOWED_ORIGIN", unset: "CORS_ALLOWED_ORIGIN", wantVarName: "CORS_ALLOWED_ORIGIN"},
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
