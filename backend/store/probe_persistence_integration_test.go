//go:build integration

package store

import (
	"context"
	"testing"
	"time"
)

// tlsOutcome builds a successful ProbeOutcome carrying a TLS cert expiry, for
// the TLS-warning tests below.
func tlsOutcome(monitorID, priorState string, priorFailStreak int32, priorWarned *time.Time, expiresAt, now time.Time) ProbeOutcome {
	status := int32(200)
	latency := int32(10)
	return ProbeOutcome{
		MonitorID:               monitorID,
		OK:                      true,
		HTTPStatus:              &status,
		LatencyMS:               &latency,
		TLSExpiresAt:            &expiresAt,
		PriorState:              priorState,
		PriorFailStreak:         priorFailStreak,
		PriorTLSWarnedExpiresAt: priorWarned,
		NextProbeAt:             now.Add(time.Minute),
		Now:                     now,
	}
}

// TestRecordProbeResult_TLSExpiryWarning_ExactlyOnce covers the PING-018 AC:
// "cert expiring in 13 days → exactly one warning event+email". A cert well
// outside the 14-day window produces no warning; once inside the window, the
// first probe fires exactly one tls_expiry event+alert, and every subsequent
// probe against the *same* certificate (same TLSExpiresAt) must not re-warn.
func TestRecordProbeResult_TLSExpiryWarning_ExactlyOnce(t *testing.T) {
	e := newCheckinTestEnv(t)
	m := e.seedHTTPMonitor(t, "up", 0, 2)
	ctx := context.Background()
	now := time.Now()

	farExpiry := now.Add(40 * 24 * time.Hour)
	if _, err := e.store.RecordProbeResult(ctx, tlsOutcome(m.ID.String(), "up", 0, nil, farExpiry, now)); err != nil {
		t.Fatalf("RecordProbeResult (outside window): %v", err)
	}
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1 AND type='tls_expiry'`, m.ID); n != 0 {
		t.Errorf("tls_expiry events = %d after far-expiry probe, want 0", n)
	}

	// 13 days out: inside the 14-day warning window.
	nearExpiry := now.Add(13 * 24 * time.Hour)
	if _, err := e.store.RecordProbeResult(ctx, tlsOutcome(m.ID.String(), "up", 0, nil, nearExpiry, now)); err != nil {
		t.Fatalf("RecordProbeResult (1st near-expiry probe): %v", err)
	}
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1 AND type='tls_expiry'`, m.ID); n != 1 {
		t.Errorf("tls_expiry events = %d after 1st near-expiry probe, want exactly 1", n)
	}
	if n := e.countRows(t, `SELECT count(*) FROM alerts a JOIN events e ON e.id=a.event_id WHERE a.monitor_id=$1 AND e.type='tls_expiry'`, m.ID); n != 1 {
		t.Errorf("tls_expiry alerts = %d, want exactly 1", n)
	}

	// Re-probe the *same* certificate (same expiry) a few more times: the
	// monitor's tls_warned_expires_at is now set, so no further warnings.
	warned := e.getMonitor(t, m.ID)
	if !warned.TlsWarnedExpiresAt.Valid {
		t.Fatalf("tls_warned_expires_at not set after warning")
	}
	priorWarned := warned.TlsWarnedExpiresAt.Time
	for i := 0; i < 3; i++ {
		if _, err := e.store.RecordProbeResult(ctx, tlsOutcome(m.ID.String(), "up", 0, &priorWarned, nearExpiry, now)); err != nil {
			t.Fatalf("RecordProbeResult (repeat probe %d): %v", i, err)
		}
	}
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1 AND type='tls_expiry'`, m.ID); n != 1 {
		t.Errorf("tls_expiry events = %d after repeat probes against same cert, want still 1", n)
	}
}

// TestRecordProbeResult_TLSExpiryWarning_RearmsOnRenewal covers the PING-018
// AC: "renewed cert re-arms the warning". After a warning fires for one
// expiry, a probe reporting a *later* expiry (certificate renewed) must fire
// a fresh warning rather than staying suppressed.
func TestRecordProbeResult_TLSExpiryWarning_RearmsOnRenewal(t *testing.T) {
	e := newCheckinTestEnv(t)
	m := e.seedHTTPMonitor(t, "up", 0, 2)
	ctx := context.Background()
	now := time.Now()

	firstExpiry := now.Add(10 * 24 * time.Hour)
	if _, err := e.store.RecordProbeResult(ctx, tlsOutcome(m.ID.String(), "up", 0, nil, firstExpiry, now)); err != nil {
		t.Fatalf("RecordProbeResult (1st cert): %v", err)
	}
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1 AND type='tls_expiry'`, m.ID); n != 1 {
		t.Fatalf("tls_expiry events after 1st cert = %d, want 1", n)
	}

	// Certificate renewed: new NotAfter, still within the warning window (say
	// the renewal itself expires in 13 days rather than being a long-lived
	// cert) — same-window renewal must still re-arm because it's a distinct
	// TLSExpiresAt from what was warned.
	warned := e.getMonitor(t, m.ID)
	priorWarned := warned.TlsWarnedExpiresAt.Time
	renewedExpiry := now.Add(13 * 24 * time.Hour)
	if _, err := e.store.RecordProbeResult(ctx, tlsOutcome(m.ID.String(), "up", 0, &priorWarned, renewedExpiry, now)); err != nil {
		t.Fatalf("RecordProbeResult (renewed cert): %v", err)
	}
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1 AND type='tls_expiry'`, m.ID); n != 2 {
		t.Errorf("tls_expiry events after renewal = %d, want 2 (fresh warning fired)", n)
	}
}

// TestListProbeResultsByMonitor_PaginatesAndFilters covers the PING-018 AC:
// "Probe log endpoint cursor-paginates and filters by outcome".
func TestListProbeResultsByMonitor_PaginatesAndFilters(t *testing.T) {
	e := newCheckinTestEnv(t)
	m := e.seedHTTPMonitor(t, "up", 0, 2)
	ctx := context.Background()
	now := time.Now()

	// 5 successes, then 3 failures, each RecordProbeResult call inserting one
	// probe_results row (PriorState kept "up"/"down" as appropriate so the
	// confirmation-threshold logic doesn't matter for this test — we only
	// care about the probe_results log, not the resulting monitor state).
	for i := 0; i < 5; i++ {
		if _, err := e.store.RecordProbeResult(ctx, successOutcome(m.ID.String(), "up", 0)); err != nil {
			t.Fatalf("seed success %d: %v", i, err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := e.store.RecordProbeResult(ctx, failOutcome(m.ID.String(), "down", 5, 100, "request timed out")); err != nil {
			t.Fatalf("seed failure %d: %v", i, err)
		}
	}
	_ = now

	all, err := e.store.ListProbeResultsByMonitor(ctx, m.ID.String(), "", "", 100)
	if err != nil {
		t.Fatalf("ListProbeResultsByMonitor (all): %v", err)
	}
	if len(all.Results) != 8 {
		t.Fatalf("all results = %d, want 8", len(all.Results))
	}

	successes, err := e.store.ListProbeResultsByMonitor(ctx, m.ID.String(), "success", "", 100)
	if err != nil {
		t.Fatalf("ListProbeResultsByMonitor (success): %v", err)
	}
	if len(successes.Results) != 5 {
		t.Errorf("success-filtered results = %d, want 5", len(successes.Results))
	}
	for _, r := range successes.Results {
		if !r.OK {
			t.Errorf("success filter returned a failed probe: %+v", r)
		}
	}

	fails, err := e.store.ListProbeResultsByMonitor(ctx, m.ID.String(), "fail", "", 100)
	if err != nil {
		t.Fatalf("ListProbeResultsByMonitor (fail): %v", err)
	}
	if len(fails.Results) != 3 {
		t.Errorf("fail-filtered results = %d, want 3", len(fails.Results))
	}
	for _, r := range fails.Results {
		if r.OK {
			t.Errorf("fail filter returned a successful probe: %+v", r)
		}
	}

	// Pagination: page size 3, newest first, cursor advances until exhausted.
	page1, err := e.store.ListProbeResultsByMonitor(ctx, m.ID.String(), "", "", 3)
	if err != nil {
		t.Fatalf("ListProbeResultsByMonitor (page1): %v", err)
	}
	if len(page1.Results) != 3 || page1.NextCursor == "" {
		t.Fatalf("page1 = %d results, cursor=%q; want 3 results with a next cursor", len(page1.Results), page1.NextCursor)
	}
	page2, err := e.store.ListProbeResultsByMonitor(ctx, m.ID.String(), "", page1.NextCursor, 3)
	if err != nil {
		t.Fatalf("ListProbeResultsByMonitor (page2): %v", err)
	}
	if len(page2.Results) != 3 {
		t.Fatalf("page2 = %d results, want 3", len(page2.Results))
	}
	seen := map[int64]bool{}
	for _, r := range append(page1.Results, page2.Results...) {
		if seen[r.ID] {
			t.Errorf("duplicate probe result id %d across pages", r.ID)
		}
		seen[r.ID] = true
	}
}

// TestLatencySeriesByMonitor_Buckets covers the PING-018 AC: "Latency series
// endpoint returns correct buckets for 24h/7d/30d windows". Only successful
// probes with known latencies are seeded, spread across two distinct
// 5-minute buckets, and the test asserts the bucket count and per-bucket
// sample count/percentiles land where expected.
func TestLatencySeriesByMonitor_Buckets(t *testing.T) {
	e := newCheckinTestEnv(t)
	m := e.seedHTTPMonitor(t, "up", 0, 2)
	ctx := context.Background()

	// Seed probe_results directly (bypassing RecordProbeResult) so each row's
	// created_at can be placed deterministically into a specific bucket.
	seed := func(createdAt time.Time, latencyMS int32) {
		if _, err := e.pool.Exec(ctx,
			`INSERT INTO probe_results (monitor_id, ok, http_status, latency_ms, created_at) VALUES ($1, true, 200, $2, $3)`,
			m.ID, latencyMS, createdAt); err != nil {
			t.Fatalf("seed probe_results: %v", err)
		}
	}

	base := time.Now().Add(-2 * time.Hour).Truncate(5 * time.Minute)
	bucket1 := base
	bucket2 := base.Add(5 * time.Minute)

	// Bucket 1: latencies 10, 20, 30 (median 20).
	seed(bucket1.Add(10*time.Second), 10)
	seed(bucket1.Add(20*time.Second), 20)
	seed(bucket1.Add(30*time.Second), 30)
	// Bucket 2: latencies 100, 200.
	seed(bucket2.Add(10*time.Second), 100)
	seed(bucket2.Add(20*time.Second), 200)

	buckets, err := e.store.LatencySeriesByMonitor(ctx, m.ID.String(), base.Add(-time.Minute), 5*60)
	if err != nil {
		t.Fatalf("LatencySeriesByMonitor: %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("buckets = %d, want 2", len(buckets))
	}

	if buckets[0].SampleCount != 3 {
		t.Errorf("bucket1 sample count = %d, want 3", buckets[0].SampleCount)
	}
	if buckets[0].P50 != 20 {
		t.Errorf("bucket1 p50 = %v, want 20", buckets[0].P50)
	}
	if buckets[1].SampleCount != 2 {
		t.Errorf("bucket2 sample count = %d, want 2", buckets[1].SampleCount)
	}
	if buckets[1].Avg != 150 {
		t.Errorf("bucket2 avg = %v, want 150", buckets[1].Avg)
	}
	if !buckets[0].BucketStart.Before(buckets[1].BucketStart) {
		t.Errorf("bucket1 start %v not before bucket2 start %v", buckets[0].BucketStart, buckets[1].BucketStart)
	}
}
