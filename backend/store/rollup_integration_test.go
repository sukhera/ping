//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sukhera/ping/db"
)

// resetRollupTables clears every table the rollup job touches so each test
// starts from a known-empty slate regardless of what other integration tests
// left behind. Integration tests own the database and run serially.
func resetRollupTables(t *testing.T) {
	t.Helper()
	env := newDailyStatsTestEnv(t)
	if _, err := env.pool.Exec(context.Background(), `TRUNCATE monitors, checkins, probe_results, events, daily_stats CASCADE`); err != nil {
		t.Fatalf("reset rollup tables: %v", err)
	}
}

func (e *dailyStatsTestEnv) seedHTTPMonitor(t *testing.T) db.Monitor {
	t.Helper()
	ctx := context.Background()

	email := "rollup+" + t.Name() + time.Now().Format("150405.000000000") + "@example.test"
	user, err := e.q.CreateUser(ctx, db.CreateUserParams{Email: email, PasswordHash: "x"})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	m, err := e.q.CreateMonitor(ctx, db.CreateMonitorParams{
		UserID:     user.ID,
		Kind:       "http",
		Slug:       "http-" + time.Now().Format("150405.000000000"),
		Name:       "http monitor",
		Url:        pgtype.Text{String: "https://example.test", Valid: true},
		Method:     pgtype.Text{String: "GET", Valid: true},
		IntervalS:  pgtype.Int4{Int32: 60, Valid: true},
		TimeoutS:   pgtype.Int4{Int32: 10, Valid: true},
		Tz:         "UTC",
		HttpConfig: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("seed http monitor: %v", err)
	}
	return m
}

func (e *dailyStatsTestEnv) insertCheckin(t *testing.T, monitorID pgtype.UUID, kind string, at time.Time) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO checkins (monitor_id, kind, created_at) VALUES ($1, $2, $3)`,
		monitorID, kind, at); err != nil {
		t.Fatalf("insert checkin: %v", err)
	}
}

func (e *dailyStatsTestEnv) insertProbeResult(t *testing.T, monitorID pgtype.UUID, ok bool, latencyMS int32, at time.Time) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO probe_results (monitor_id, ok, latency_ms, created_at) VALUES ($1, $2, $3, $4)`,
		monitorID, ok, latencyMS, at); err != nil {
		t.Fatalf("insert probe result: %v", err)
	}
}

func (e *dailyStatsTestEnv) insertEvent(t *testing.T, monitorID pgtype.UUID, eventType string, at time.Time) int64 {
	t.Helper()
	var id int64
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO events (monitor_id, type, message, created_at) VALUES ($1, $2, $3, $4) RETURNING id`,
		monitorID, eventType, "test event", at).Scan(&id); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	return id
}

func (e *dailyStatsTestEnv) countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

func (e *dailyStatsTestEnv) getDailyStat(t *testing.T, monitorID pgtype.UUID, day time.Time) db.DailyStat {
	t.Helper()
	rows, err := e.store.ListDailyStats(context.Background(), []string{monitorID.String()}, day)
	if err != nil {
		t.Fatalf("list daily stats: %v", err)
	}
	stats := rows[monitorID.String()]
	for _, s := range stats {
		if s.Day.Equal(day) {
			return db.DailyStat{
				MonitorID:  monitorID,
				Day:        pgtype.Date{Time: s.Day, Valid: true},
				Checkins:   s.Checkins,
				Failures:   s.Failures,
				DowntimeS:  s.DowntimeS,
				LatencyP50: nullableInt32(s.LatencyP50),
				LatencyP95: nullableInt32(s.LatencyP95),
			}
		}
	}
	t.Fatalf("no daily_stats row for monitor %s day %s", monitorID.String(), day.Format(time.DateOnly))
	return db.DailyStat{}
}

// TestRollupDay_HeartbeatCheckinCounts is the basic AC path: a heartbeat
// monitor's checkins/fails for a day roll up into one daily_stats row.
func TestRollupDay_HeartbeatCheckinCounts(t *testing.T) {
	resetRollupTables(t)
	env := newDailyStatsTestEnv(t)
	m := env.seedMonitor(t)

	day := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	env.insertCheckin(t, m.ID, "success", day.Add(1*time.Hour))
	env.insertCheckin(t, m.ID, "success", day.Add(2*time.Hour))
	env.insertCheckin(t, m.ID, "fail", day.Add(3*time.Hour))

	res, err := env.store.RollupDay(context.Background(), day)
	if err != nil {
		t.Fatalf("RollupDay: %v", err)
	}
	if res.MonitorsUpdated != 1 {
		t.Fatalf("MonitorsUpdated = %d, want 1", res.MonitorsUpdated)
	}

	stat := env.getDailyStat(t, m.ID, day)
	if stat.Checkins != 3 || stat.Failures != 1 {
		t.Errorf("stat = %+v, want checkins=3 failures=1", stat)
	}
}

// TestRollupDay_HTTPProbeLatencyPercentiles confirms an HTTP monitor's probe
// counts and latency p50/p95 land in the rollup, and that a day with zero
// successful probes yields NULL percentiles rather than crashing.
func TestRollupDay_HTTPProbeLatencyPercentiles(t *testing.T) {
	resetRollupTables(t)
	env := newDailyStatsTestEnv(t)
	m := env.seedHTTPMonitor(t)

	day := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	for _, lat := range []int32{100, 200, 300, 400, 500} {
		env.insertProbeResult(t, m.ID, true, lat, day.Add(time.Hour))
	}
	env.insertProbeResult(t, m.ID, false, 0, day.Add(2*time.Hour))

	if _, err := env.store.RollupDay(context.Background(), day); err != nil {
		t.Fatalf("RollupDay: %v", err)
	}

	stat := env.getDailyStat(t, m.ID, day)
	if stat.Checkins != 6 || stat.Failures != 1 {
		t.Errorf("stat = %+v, want checkins=6 failures=1", stat)
	}
	if !stat.LatencyP50.Valid || !stat.LatencyP95.Valid {
		t.Fatalf("stat latency percentiles not set: %+v", stat)
	}
	if stat.LatencyP50.Int32 != 300 {
		t.Errorf("LatencyP50 = %d, want 300 (median of 100..500)", stat.LatencyP50.Int32)
	}

	// A monitor with only failed probes that day: percentiles must be NULL,
	// not a scan panic.
	m2 := env.seedHTTPMonitor(t)
	env.insertProbeResult(t, m2.ID, false, 0, day.Add(time.Hour))
	if _, err := env.store.RollupDay(context.Background(), day); err != nil {
		t.Fatalf("RollupDay (all-fail monitor): %v", err)
	}
	stat2 := env.getDailyStat(t, m2.ID, day)
	if stat2.LatencyP50.Valid || stat2.LatencyP95.Valid {
		t.Errorf("stat2 = %+v, want NULL percentiles for an all-failed day", stat2)
	}
}

// TestRollupDay_Idempotent is AC-1: re-running the rollup for the same day
// replaces rather than double-counts.
func TestRollupDay_Idempotent(t *testing.T) {
	resetRollupTables(t)
	env := newDailyStatsTestEnv(t)
	m := env.seedMonitor(t)

	day := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	env.insertCheckin(t, m.ID, "success", day.Add(time.Hour))

	if _, err := env.store.RollupDay(context.Background(), day); err != nil {
		t.Fatalf("first RollupDay: %v", err)
	}
	if _, err := env.store.RollupDay(context.Background(), day); err != nil {
		t.Fatalf("second RollupDay: %v", err)
	}

	stat := env.getDailyStat(t, m.ID, day)
	if stat.Checkins != 1 {
		t.Errorf("Checkins after re-running rollup = %d, want 1 (not doubled)", stat.Checkins)
	}
}

// TestRollupDay_DowntimeAcrossDayBoundary is AC-2: an outage that starts
// before midnight and ends after it must split its downtime seconds across
// both days when each is rolled up independently.
func TestRollupDay_DowntimeAcrossDayBoundary(t *testing.T) {
	resetRollupTables(t)
	env := newDailyStatsTestEnv(t)
	m := env.seedMonitor(t)

	day1 := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	day2 := day1.AddDate(0, 0, 1)

	// Down at 23:00 on day1, recovers at 01:00 on day2: 1h of downtime in
	// day1, 1h of downtime in day2.
	env.insertEvent(t, m.ID, "down", day1.Add(23*time.Hour))
	env.insertEvent(t, m.ID, "up", day2.Add(1*time.Hour))

	if _, err := env.store.RollupDay(context.Background(), day1); err != nil {
		t.Fatalf("RollupDay(day1): %v", err)
	}
	if _, err := env.store.RollupDay(context.Background(), day2); err != nil {
		t.Fatalf("RollupDay(day2): %v", err)
	}

	stat1 := env.getDailyStat(t, m.ID, day1)
	stat2 := env.getDailyStat(t, m.ID, day2)

	if stat1.DowntimeS != 3600 {
		t.Errorf("day1 DowntimeS = %d, want 3600 (23:00-24:00)", stat1.DowntimeS)
	}
	if stat2.DowntimeS != 3600 {
		t.Errorf("day2 DowntimeS = %d, want 3600 (00:00-01:00)", stat2.DowntimeS)
	}
}

// TestRollupDay_DowntimeStillOpenCountsThroughDayEnd confirms an outage with
// no recovery event yet (still down) counts as downtime through the end of
// the rolled-up day, not zero.
func TestRollupDay_DowntimeStillOpenCountsThroughDayEnd(t *testing.T) {
	resetRollupTables(t)
	env := newDailyStatsTestEnv(t)
	m := env.seedMonitor(t)

	day := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	env.insertEvent(t, m.ID, "down", day.Add(22*time.Hour))

	if _, err := env.store.RollupDay(context.Background(), day); err != nil {
		t.Fatalf("RollupDay: %v", err)
	}

	stat := env.getDailyStat(t, m.ID, day)
	if stat.DowntimeS != 7200 {
		t.Errorf("DowntimeS = %d, want 7200 (22:00-24:00, still down)", stat.DowntimeS)
	}
}

// TestPruneOldData_DeletesOnlyOlderThanCutoff confirms rows on either side of
// the cutoff are handled correctly: older rows removed, newer rows kept.
func TestPruneOldData_DeletesOnlyOlderThanCutoff(t *testing.T) {
	resetRollupTables(t)
	env := newDailyStatsTestEnv(t)
	m := env.seedMonitor(t)

	cutoff := time.Now().UTC().AddDate(0, 0, -90)
	env.insertCheckin(t, m.ID, "success", cutoff.Add(-time.Hour)) // old: pruned
	env.insertCheckin(t, m.ID, "success", cutoff.Add(time.Hour))  // recent: kept
	env.insertProbeResult(t, m.ID, true, 100, cutoff.Add(-time.Hour))
	env.insertProbeResult(t, m.ID, true, 100, cutoff.Add(time.Hour))
	env.insertEvent(t, m.ID, "down", cutoff.Add(-time.Hour))
	env.insertEvent(t, m.ID, "up", cutoff.Add(time.Hour))

	res, err := env.store.PruneOldData(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("PruneOldData: %v", err)
	}
	if res.Checkins != 1 || res.ProbeResults != 1 || res.Events != 1 {
		t.Errorf("PruneResult = %+v, want 1 row deleted from each table", res)
	}

	remainingCheckins := env.countRows(t, `SELECT COUNT(*) FROM checkins WHERE monitor_id = $1`, m.ID)
	if remainingCheckins != 1 {
		t.Errorf("remaining checkins = %d, want 1 (the recent one)", remainingCheckins)
	}
}

// TestPruneOldData_NeverTouchesDailyStats is part of AC-3: pruning must never
// remove daily_stats rows regardless of how old they are.
func TestPruneOldData_NeverTouchesDailyStats(t *testing.T) {
	resetRollupTables(t)
	env := newDailyStatsTestEnv(t)
	m := env.seedMonitor(t)

	veryOld := time.Now().UTC().AddDate(-1, 0, 0)
	env.seedDailyStat(t, m.ID, veryOld, 5, 1, 60)

	cutoff := time.Now().UTC().AddDate(0, 0, -90)
	if _, err := env.store.PruneOldData(context.Background(), cutoff); err != nil {
		t.Fatalf("PruneOldData: %v", err)
	}

	remaining := env.countRows(t, `SELECT COUNT(*) FROM daily_stats WHERE monitor_id = $1`, m.ID)
	if remaining != 1 {
		t.Errorf("daily_stats rows remaining = %d, want 1 (never pruned)", remaining)
	}
}

// TestPruneOldData_NeverDeletesEventWithPendingAlert confirms an old event
// that still has a pending outbox alert is skipped by pruning even though
// it's past the cutoff — in steady state this never happens (alerts resolve
// within minutes), but the guard must hold if it ever does.
func TestPruneOldData_NeverDeletesEventWithPendingAlert(t *testing.T) {
	resetRollupTables(t)
	env := newDailyStatsTestEnv(t)
	m := env.seedMonitor(t)

	cutoff := time.Now().UTC().AddDate(0, 0, -90)
	oldEventID := env.insertEvent(t, m.ID, "down", cutoff.Add(-time.Hour))
	if _, err := env.pool.Exec(context.Background(),
		`INSERT INTO alerts (monitor_id, event_id, channel, status) VALUES ($1, $2, 'default', 'pending')`,
		m.ID, oldEventID); err != nil {
		t.Fatalf("insert pending alert: %v", err)
	}

	if _, err := env.store.PruneOldData(context.Background(), cutoff); err != nil {
		t.Fatalf("PruneOldData: %v", err)
	}

	remaining := env.countRows(t, `SELECT COUNT(*) FROM events WHERE id = $1`, oldEventID)
	if remaining != 1 {
		t.Error("event with pending alert was pruned, want it preserved")
	}
}

// TestPruneOldData_BatchesUnderLimit seeds more than one batch's worth of old
// checkins and confirms all are eventually removed via the drain loop (AC:
// batches <= 5000 rows per statement, verified indirectly here by seeding a
// small multiple of a much smaller effective limit isn't possible without
// exposing the const, so this asserts the end-to-end result: prune fully
// drains regardless of row count).
func TestPruneOldData_DrainsAllMatchingRows(t *testing.T) {
	resetRollupTables(t)
	env := newDailyStatsTestEnv(t)
	m := env.seedMonitor(t)

	cutoff := time.Now().UTC().AddDate(0, 0, -90)
	const n = 50
	for i := 0; i < n; i++ {
		env.insertCheckin(t, m.ID, "success", cutoff.Add(-time.Duration(i+1)*time.Minute))
	}

	res, err := env.store.PruneOldData(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("PruneOldData: %v", err)
	}
	if res.Checkins != n {
		t.Errorf("Checkins pruned = %d, want %d", res.Checkins, n)
	}

	remaining := env.countRows(t, `SELECT COUNT(*) FROM checkins WHERE monitor_id = $1`, m.ID)
	if remaining != 0 {
		t.Errorf("remaining checkins = %d, want 0", remaining)
	}
}
