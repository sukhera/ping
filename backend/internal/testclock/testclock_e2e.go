//go:build e2e

// Package testclock provides a globally skewable clock used only by e2e test
// builds: the /test/advance-clock endpoint (backend/server/testclock.go) lets
// Playwright cross real deadlines (heartbeat grace periods, probe intervals)
// instantly instead of sleeping in wall-clock time. Never linked into
// production or dev binaries — this file only compiles with `-tags e2e`; see
// testclock_notag.go for the no-op stub used otherwise.
package testclock

import (
	"sync/atomic"
	"time"
)

var offsetNanos atomic.Int64

// Now returns time.Now() shifted by the accumulated offset from Advance.
func Now() time.Time {
	return time.Now().Add(time.Duration(offsetNanos.Load()))
}

// Advance shifts the clock forward by d and returns the new Now().
func Advance(d time.Duration) time.Time {
	offsetNanos.Add(int64(d))
	return Now()
}
