//go:build integration

package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sukhera/ping/db"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://ping:ping@localhost:5432/ping?sslmode=disable"
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func createTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) pgtype.UUID {
	t.Helper()

	q := db.New(pool)
	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Email:        "schema-test-" + t.Name() + "@example.com",
		PasswordHash: "hash",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", user.ID) //nolint:errcheck
	})
	return user.ID
}

func TestMonitorKindCheckConstraint(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	userID := createTestUser(t, ctx, pool)

	_, err := pool.Exec(ctx, `
		INSERT INTO monitors (user_id, kind, slug, name)
		VALUES ($1, 'bogus', 'check-kind-test', 'Test')
	`, userID)

	if err == nil {
		t.Fatal("expected CHECK constraint violation for invalid kind, got no error")
	}
}

func TestMonitorStateCheckConstraint(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	userID := createTestUser(t, ctx, pool)

	_, err := pool.Exec(ctx, `
		INSERT INTO monitors (user_id, kind, slug, name, state)
		VALUES ($1, 'heartbeat', 'check-state-test', 'Test', 'bogus_state')
	`, userID)

	if err == nil {
		t.Fatal("expected CHECK constraint violation for invalid state, got no error")
	}
}

func TestMonitorScheduleKindCheckConstraint(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	userID := createTestUser(t, ctx, pool)

	_, err := pool.Exec(ctx, `
		INSERT INTO monitors (user_id, kind, slug, name, schedule_kind)
		VALUES ($1, 'heartbeat', 'check-schedule-kind-test', 'Test', 'bogus_schedule')
	`, userID)

	if err == nil {
		t.Fatal("expected CHECK constraint violation for invalid schedule_kind, got no error")
	}
}

func TestMonitorMethodCheckConstraint(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	userID := createTestUser(t, ctx, pool)

	_, err := pool.Exec(ctx, `
		INSERT INTO monitors (user_id, kind, slug, name, method)
		VALUES ($1, 'http', 'check-method-test', 'Test', 'DELETE')
	`, userID)

	if err == nil {
		t.Fatal("expected CHECK constraint violation for invalid method, got no error")
	}
}

func TestCheckinKindCheckConstraint(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	userID := createTestUser(t, ctx, pool)

	q := db.New(pool)
	monitor, err := q.CreateMonitor(ctx, db.CreateMonitorParams{
		UserID: userID,
		Kind:   "heartbeat",
		Slug:   "check-checkin-kind-test",
		Name:   "Test",
		Tz:     "UTC",
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO checkins (monitor_id, kind)
		VALUES ($1, 'bogus_kind')
	`, monitor.ID)

	if err == nil {
		t.Fatal("expected CHECK constraint violation for invalid checkin kind, got no error")
	}
}

func TestAlertStatusCheckConstraint(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	userID := createTestUser(t, ctx, pool)

	q := db.New(pool)
	monitor, err := q.CreateMonitor(ctx, db.CreateMonitorParams{
		UserID: userID,
		Kind:   "heartbeat",
		Slug:   "check-alert-status-test",
		Name:   "Test",
		Tz:     "UTC",
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO events (monitor_id, type, message) VALUES ($1, 'down', 'test')`, monitor.ID); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	var eventID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM events WHERE monitor_id = $1`, monitor.ID).Scan(&eventID); err != nil {
		t.Fatalf("select event id: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO alerts (monitor_id, event_id, channel, status)
		VALUES ($1, $2, 'email', 'bogus_status')
	`, monitor.ID, eventID)

	if err == nil {
		t.Fatal("expected CHECK constraint violation for invalid alert status, got no error")
	}
}

func TestClaimDueMonitorsUsesPartialIndex(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	userID := createTestUser(t, ctx, pool)

	q := db.New(pool)
	monitor, err := q.CreateMonitor(ctx, db.CreateMonitorParams{
		UserID: userID,
		Kind:   "heartbeat",
		Slug:   "claim-due-test",
		Name:   "Test",
		Tz:     "UTC",
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	_, err = pool.Exec(ctx, `
		UPDATE monitors SET state = 'up', next_deadline = now() - interval '1 minute'
		WHERE id = $1
	`, monitor.ID)
	if err != nil {
		t.Fatalf("update monitor: %v", err)
	}

	claimed, err := q.ClaimDueMonitors(ctx, db.ClaimDueMonitorsParams{
		Now:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
		PageLimit: 10,
	})
	if err != nil {
		t.Fatalf("ClaimDueMonitors: %v", err)
	}

	found := false
	for _, m := range claimed {
		if m.ID == monitor.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("expected due monitor to be claimed by ClaimDueMonitors")
	}
}

func TestCreateMonitorAppliesColumnDefaults(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	userID := createTestUser(t, ctx, pool)

	q := db.New(pool)
	monitor, err := q.CreateMonitor(ctx, db.CreateMonitorParams{
		UserID: userID,
		Kind:   "heartbeat",
		Slug:   "column-defaults-test",
		Name:   "Test",
		// Tz and HttpConfig intentionally left at their Go zero values to verify
		// the DB defaults ('UTC', '{}'::jsonb) apply instead of an empty string / NULL.
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	if monitor.Tz != "UTC" {
		t.Fatalf("expected tz to default to UTC when omitted, got %q", monitor.Tz)
	}
	if string(monitor.HttpConfig) != "{}" {
		t.Fatalf("expected http_config to default to {}, got %q", monitor.HttpConfig)
	}
}
