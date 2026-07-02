//go:build integration

package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

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

// getHealth serves /health and returns the status code + decoded body.
func getHealth(t *testing.T, deps server.Deps) (int, map[string]any) {
	t.Helper()
	srv := server.New(":0", deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	srv.Handler.ServeHTTP(rec, req)

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return rec.Code, body
}

// schedulerComponent extracts the scheduler component's status string.
func schedulerComponent(t *testing.T, body map[string]any) string {
	t.Helper()
	comps, ok := body["components"].(map[string]any)
	if !ok {
		t.Fatalf("no components in body: %v", body)
	}
	sched, ok := comps["scheduler"].(map[string]any)
	if !ok {
		t.Fatalf("no scheduler component: %v", comps)
	}
	return sched["status"].(string)
}

func TestHealth_SchedulerHeartbeatFresh(t *testing.T) {
	deps, _ := testDeps(t)
	rdb := deps.Redis
	// Fresh heartbeat: written just now.
	if err := rdb.Set(context.Background(), "worker:heartbeat:scheduler",
		strconv.FormatInt(time.Now().Unix(), 10), time.Minute).Err(); err != nil {
		t.Fatalf("set heartbeat: %v", err)
	}
	t.Cleanup(func() { rdb.Del(context.Background(), "worker:heartbeat:scheduler") })

	code, body := getHealth(t, deps)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %v", code, body)
	}
	if got := schedulerComponent(t, body); got != "up" {
		t.Errorf("scheduler = %q, want up", got)
	}
}

func TestHealth_503WhenSchedulerHeartbeatStale(t *testing.T) {
	deps, _ := testDeps(t)
	rdb := deps.Redis
	// Stale heartbeat: 90s old (> 60s threshold), but the key still exists.
	if err := rdb.Set(context.Background(), "worker:heartbeat:scheduler",
		strconv.FormatInt(time.Now().Add(-90*time.Second).Unix(), 10), time.Minute).Err(); err != nil {
		t.Fatalf("set heartbeat: %v", err)
	}
	t.Cleanup(func() { rdb.Del(context.Background(), "worker:heartbeat:scheduler") })

	code, body := getHealth(t, deps)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %v", code, body)
	}
	if got := schedulerComponent(t, body); got != "down" {
		t.Errorf("scheduler = %q, want down", got)
	}
}

func TestHealth_MissingHeartbeatDoesNotFail(t *testing.T) {
	deps, _ := testDeps(t)
	// Ensure no scheduler heartbeat exists — a --role=api deployment must not 503.
	if err := deps.Redis.Del(context.Background(), "worker:heartbeat:scheduler").Err(); err != nil {
		t.Fatalf("del heartbeat: %v", err)
	}

	code, body := getHealth(t, deps)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (missing heartbeat is not a failure); body = %v", code, body)
	}
	if got := schedulerComponent(t, body); got != "unknown" {
		t.Errorf("scheduler = %q, want unknown", got)
	}
}
