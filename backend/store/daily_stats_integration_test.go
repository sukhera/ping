//go:build integration

package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sukhera/ping/db"
)

// dailyStatsTestEnv mirrors checkinTestEnv (checkin_integration_test.go): a
// store wired to a real pool plus the pool itself, so tests can seed
// daily_stats rows directly (no store write path exists yet — that's
// PING-020's rollup job, not built).
type dailyStatsTestEnv struct {
	store *Store
	pool  *pgxpool.Pool
	q     *db.Queries
}

func newDailyStatsTestEnv(t *testing.T) *dailyStatsTestEnv {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://ping:ping@localhost:5432/ping?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	return &dailyStatsTestEnv{store: New(pool, nil), pool: pool, q: db.New(pool)}
}

func (e *dailyStatsTestEnv) seedMonitor(t *testing.T) db.Monitor {
	t.Helper()
	ctx := context.Background()

	email := "dailystats+" + strings.ReplaceAll(t.Name(), "/", "_") + time.Now().Format("150405.000000") + "@example.test"
	user, err := e.q.CreateUser(ctx, db.CreateUserParams{Email: email, PasswordHash: "x"})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	m, err := e.q.CreateMonitor(ctx, db.CreateMonitorParams{
		UserID:       user.ID,
		Kind:         "heartbeat",
		Slug:         "slug-" + time.Now().Format("150405.000000000"),
		Name:         "test monitor",
		ScheduleKind: pgtype.Text{String: "period", Valid: true},
		PeriodS:      pgtype.Int4{Int32: 300, Valid: true},
		GraceS:       pgtype.Int4{Int32: 60, Valid: true},
		Tz:           "UTC",
	})
	if err != nil {
		t.Fatalf("seed monitor: %v", err)
	}
	return m
}

func (e *dailyStatsTestEnv) seedDailyStat(t *testing.T, monitorID pgtype.UUID, day time.Time, checkins, failures, downtimeS int32) {
	t.Helper()
	_, err := e.pool.Exec(context.Background(),
		`INSERT INTO daily_stats (monitor_id, day, checkins, failures, downtime_s) VALUES ($1, $2, $3, $4, $5)`,
		monitorID, day, checkins, failures, downtimeS)
	if err != nil {
		t.Fatalf("seed daily_stats row: %v", err)
	}
}

// TestListDailyStats_ReturnsSeededRowsBucketedByMonitor is the PING-013
// uptime-bar data-path test: a directly-seeded daily_stats row (no store
// write method exists yet — see the doc comment above) round-trips through
// Store.ListDailyStats correctly bucketed by monitor id.
func TestListDailyStats_ReturnsSeededRowsBucketedByMonitor(t *testing.T) {
	env := newDailyStatsTestEnv(t)
	monitorA := env.seedMonitor(t)
	monitorB := env.seedMonitor(t)

	today := time.Now().UTC().Truncate(24 * time.Hour)
	yesterday := today.AddDate(0, 0, -1)
	env.seedDailyStat(t, monitorA.ID, yesterday, 10, 0, 0)
	env.seedDailyStat(t, monitorA.ID, today, 5, 1, 120)
	// monitorB gets no daily_stats rows at all — the common case today, since
	// nothing populates this table until PING-020 ships.

	since := today.AddDate(0, 0, -89)
	got, err := env.store.ListDailyStats(context.Background(), []string{monitorA.ID.String(), monitorB.ID.String()}, since)
	if err != nil {
		t.Fatalf("ListDailyStats: %v", err)
	}

	aStats, ok := got[monitorA.ID.String()]
	if !ok || len(aStats) != 2 {
		t.Fatalf("monitor A stats = %+v, want 2 rows", aStats)
	}
	if aStats[0].Day.Before(aStats[1].Day) == false {
		// ORDER BY monitor_id, day ASC — yesterday should come before today.
		t.Errorf("monitor A stats not ordered oldest-first: %+v", aStats)
	}
	if aStats[1].Checkins != 5 || aStats[1].Failures != 1 || aStats[1].DowntimeS != 120 {
		t.Errorf("monitor A today's stat = %+v, want checkins=5 failures=1 downtime_s=120", aStats[1])
	}

	if bStats, ok := got[monitorB.ID.String()]; ok {
		t.Errorf("monitor B (zero seeded rows) present in result = %+v, want absent, not an error", bStats)
	}
}

// TestListDailyStats_SinceFiltersOlderDays confirms the "since" cutoff
// actually excludes rows outside the 90-day window rather than returning
// everything.
func TestListDailyStats_SinceFiltersOlderDays(t *testing.T) {
	env := newDailyStatsTestEnv(t)
	monitor := env.seedMonitor(t)

	today := time.Now().UTC().Truncate(24 * time.Hour)
	tooOld := today.AddDate(0, 0, -100)
	env.seedDailyStat(t, monitor.ID, tooOld, 1, 0, 0)
	env.seedDailyStat(t, monitor.ID, today, 1, 0, 0)

	since := today.AddDate(0, 0, -89)
	got, err := env.store.ListDailyStats(context.Background(), []string{monitor.ID.String()}, since)
	if err != nil {
		t.Fatalf("ListDailyStats: %v", err)
	}

	stats := got[monitor.ID.String()]
	if len(stats) != 1 {
		t.Fatalf("stats = %+v, want exactly 1 row (the too-old row must be excluded)", stats)
	}
}

// TestListDailyStats_EmptyMonitorIDs_ReturnsEmptyMapNoQuery guards the
// empty-page short-circuit in the server list handler — calling with zero
// monitor ids must not error (and, per the store implementation, skips the
// SQL round-trip entirely).
func TestListDailyStats_EmptyMonitorIDs_ReturnsEmptyMapNoQuery(t *testing.T) {
	env := newDailyStatsTestEnv(t)

	got, err := env.store.ListDailyStats(context.Background(), []string{}, time.Now())
	if err != nil {
		t.Fatalf("ListDailyStats: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got = %+v, want empty map", got)
	}
}
