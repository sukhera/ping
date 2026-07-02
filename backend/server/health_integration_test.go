//go:build integration

package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/sukhera/ping/server"
)

func testDeps(t *testing.T) (server.Deps, *pgxpool.Pool) {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://ping:ping@localhost:5432/ping?sslmode=disable"
	}
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379/0"
	}

	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("redis.ParseURL: %v", err)
	}
	rdb := redis.NewClient(redisOpts)
	t.Cleanup(func() { rdb.Close() }) //nolint:errcheck

	return server.Deps{DB: pool, Redis: rdb}, pool
}

func TestHealth_OKWhenDependenciesUp(t *testing.T) {
	deps, _ := testDeps(t)
	srv := server.New(":0", deps)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status field = %v, want ok", body["status"])
	}
}

func TestHealth_503WhenPostgresDown(t *testing.T) {
	deps, pool := testDeps(t)
	pool.Close() // simulate Postgres being unreachable

	srv := server.New(":0", deps)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}
