package store

import (
	"context"
	"fmt"
	"time"
)

// Allow implements a fixed-window counter via Redis INCR+EXPIRE. key should
// already be fully qualified, e.g. "rate:login:203.0.113.5".
//
// On Redis failure it fails open: allowed=true is returned alongside a
// non-nil error, so a Redis outage never blocks legitimate traffic. Callers
// should log the error and proceed rather than treat it as a hard failure.
func (s *Store) Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error) {
	if s.redis == nil {
		return true, 0, nil
	}

	count, err := s.redis.Incr(ctx, key).Result()
	if err != nil {
		return true, 0, fmt.Errorf("store: rate limit incr: %w", err)
	}

	if count == 1 {
		if err := s.redis.Expire(ctx, key, window).Err(); err != nil {
			return true, 0, fmt.Errorf("store: rate limit expire: %w", err)
		}
	}

	if count > int64(limit) {
		ttl, ttlErr := s.redis.TTL(ctx, key).Result()
		if ttlErr != nil || ttl < 0 {
			ttl = window
		}
		return false, ttl, nil
	}

	return true, 0, nil
}
