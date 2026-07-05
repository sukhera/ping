//go:build !e2e

package testclock

import "time"

// Now is real wall-clock time in every non-e2e build (dev, prod, unit/integration
// tests) — swapping call sites to testclock.Now() is a no-op behavior change here.
func Now() time.Time {
	return time.Now()
}

// Advance is unreachable outside e2e builds: no route ever calls it, since
// backend/server/testclock.go itself only compiles with `-tags e2e`.
func Advance(d time.Duration) time.Time {
	return Now()
}
