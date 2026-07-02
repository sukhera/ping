//go:build integration

package store_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/sukhera/ping/store"
)

func testRedisStore(t *testing.T) *store.Store {
	t.Helper()

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379/0"
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("redis.ParseURL: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { rdb.Close() }) //nolint:errcheck

	return store.New(nil, rdb)
}

func uniqueRateLimitKey(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("rate:test:%s:%d", t.Name(), time.Now().UnixNano())
}

func TestAllow_PermitsUpToLimit(t *testing.T) {
	s := testRedisStore(t)
	key := uniqueRateLimitKey(t)

	for i := 1; i <= 5; i++ {
		allowed, _, err := s.Allow(context.Background(), key, 5, time.Minute)
		if err != nil {
			t.Fatalf("attempt %d: unexpected error: %v", i, err)
		}
		if !allowed {
			t.Fatalf("attempt %d: allowed = false, want true (within limit)", i)
		}
	}
}

func TestAllow_BlocksSixthAttemptWithRetryAfter(t *testing.T) {
	s := testRedisStore(t)
	key := uniqueRateLimitKey(t)

	for i := 1; i <= 5; i++ {
		if _, _, err := s.Allow(context.Background(), key, 5, time.Minute); err != nil {
			t.Fatalf("attempt %d: unexpected error: %v", i, err)
		}
	}

	allowed, retryAfter, err := s.Allow(context.Background(), key, 5, time.Minute)
	if err != nil {
		t.Fatalf("6th attempt: unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("6th attempt: allowed = true, want false")
	}
	if retryAfter <= 0 || retryAfter > time.Minute {
		t.Errorf("retryAfter = %v, want a positive duration <= window", retryAfter)
	}
}

func TestAllow_IndependentKeysHaveIndependentBudgets(t *testing.T) {
	s := testRedisStore(t)
	keyA := uniqueRateLimitKey(t) + ":a"
	keyB := uniqueRateLimitKey(t) + ":b"

	for i := 1; i <= 5; i++ {
		if _, _, err := s.Allow(context.Background(), keyA, 5, time.Minute); err != nil {
			t.Fatalf("keyA attempt %d: unexpected error: %v", i, err)
		}
	}
	allowedA, _, _ := s.Allow(context.Background(), keyA, 5, time.Minute)
	if allowedA {
		t.Fatal("keyA 6th attempt: allowed = true, want false (exhausted)")
	}

	allowedB, _, err := s.Allow(context.Background(), keyB, 5, time.Minute)
	if err != nil {
		t.Fatalf("keyB: unexpected error: %v", err)
	}
	if !allowedB {
		t.Fatal("keyB first attempt: allowed = false, want true (independent budget)")
	}
}
