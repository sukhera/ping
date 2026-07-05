package prober

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sukhera/ping/store"
)

type fakeStore struct {
	monitors []store.Monitor
	claimed  atomic.Int64

	results atomic.Int64
}

func (f *fakeStore) ClaimDueProbes(context.Context, time.Time, int32) ([]store.Monitor, error) {
	// Only the first call returns monitors; subsequent ticks see none, so the
	// loop test's monitor count doesn't grow unbounded across ticks.
	if f.claimed.Add(1) > 1 {
		return nil, nil
	}
	return f.monitors, nil
}

func (f *fakeStore) RecordProbeResult(context.Context, store.ProbeOutcome) (store.RecordProbeResultResult, error) {
	f.results.Add(1)
	return store.RecordProbeResultResult{}, nil
}

func newHTTPMonitor(id, slug, url string) store.Monitor {
	threshold := int32(2)
	timeout := int32(5)
	interval := int32(60)
	return store.Monitor{
		ID:            id,
		Kind:          "http",
		Slug:          slug,
		URL:           url,
		Method:        "GET",
		State:         "up",
		TimeoutS:      &timeout,
		IntervalS:     &interval,
		FailThreshold: &threshold,
		HTTPConfig:    []byte(`{}`),
	}
}

// TestRun_TicksUntilCancelled mirrors scheduler_test.go's loop test: Run must
// stop cleanly on context cancellation.
func TestRun_TicksUntilCancelled(t *testing.T) {
	st := &fakeStore{}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- Run(ctx, st, nil, 5*time.Millisecond, nil) }()

	time.Sleep(60 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after cancel")
	}

	if st.claimed.Load() < 2 {
		t.Errorf("ClaimDueProbes called %d times, want at least 2", st.claimed.Load())
	}
}

// TestTick_BoundsConcurrency verifies the errgroup pool never runs more than
// poolLimit probes at once, per PING-017's AC (100 due monitors with slow
// targets must not exceed the configured pool bound).
func TestTick_BoundsConcurrency(t *testing.T) {
	var inFlight, maxInFlight atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		for {
			cur := maxInFlight.Load()
			if n <= cur || maxInFlight.CompareAndSwap(cur, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		inFlight.Add(-1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	const monitorCount = 100
	monitors := make([]store.Monitor, monitorCount)
	for i := range monitors {
		monitors[i] = newHTTPMonitor("mon", "slug", srv.URL)
	}

	st := &fakeStore{monitors: monitors}
	// httptest servers bind to 127.0.0.1, which the SSRF guard rejects by
	// design; allow loopback explicitly via New's allowlist so this test
	// exercises pool bounding rather than the guard.
	p := New(st, []netip.Prefix{
		netip.MustParsePrefix("127.0.0.1/32"),
		netip.MustParsePrefix("::1/128"),
	})

	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick returned %v, want nil", err)
	}

	if got := st.results.Load(); got != monitorCount {
		t.Errorf("RecordProbeResult called %d times, want %d", got, monitorCount)
	}
	if max := maxInFlight.Load(); max > poolLimit {
		t.Errorf("max concurrent probes = %d, want <= %d", max, poolLimit)
	}
}

// TestDecodeHTTPConfig_EmptyIsDefaults matches CreateMonitor's default
// http_config of '{}' — must decode to the zero value, not error.
func TestDecodeHTTPConfig_EmptyIsDefaults(t *testing.T) {
	cfg, err := decodeHTTPConfig([]byte(`{}`))
	if err != nil {
		t.Fatalf("decodeHTTPConfig returned %v, want nil", err)
	}
	if !cfg.followRedirects() {
		t.Error("followRedirects() = false, want true (default)")
	}
}

func TestDecodeHTTPConfig_Invalid(t *testing.T) {
	if _, err := decodeHTTPConfig([]byte(`not json`)); err == nil {
		t.Error("decodeHTTPConfig(invalid) returned nil error, want non-nil")
	}
}
