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

// checkinTestEnv holds a store wired to a real pool plus the pool itself, so
// tests can both exercise RecordCheckin and inspect rows directly.
type checkinTestEnv struct {
	store *Store
	pool  *pgxpool.Pool
	q     *db.Queries
}

func newCheckinTestEnv(t *testing.T) *checkinTestEnv {
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

	return &checkinTestEnv{store: New(pool, nil), pool: pool, q: db.New(pool)}
}

// seedMonitor creates a user and a heartbeat monitor in the given initial
// state, returning the monitor. period_s=300, grace_s=60 (schedule minimums).
func (e *checkinTestEnv) seedMonitor(t *testing.T, state string) db.Monitor {
	t.Helper()
	ctx := context.Background()

	email := "checkin+" + strings.ReplaceAll(t.Name(), "/", "_") + time.Now().Format("150405.000000") + "@example.test"
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

	if state != "new" {
		if _, err := e.pool.Exec(ctx, `UPDATE monitors SET state=$1 WHERE id=$2`, state, m.ID); err != nil {
			t.Fatalf("set monitor state: %v", err)
		}
		m.State = state
	}
	return m
}

func (e *checkinTestEnv) getMonitor(t *testing.T, id pgtype.UUID) db.Monitor {
	t.Helper()
	m, err := e.q.GetMonitorByID(context.Background(), id)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	return m
}

func (e *checkinTestEnv) countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

func TestRecordCheckin_UnknownSlug(t *testing.T) {
	e := newCheckinTestEnv(t)

	_, err := e.store.RecordCheckin(context.Background(), RecordCheckinParams{
		Slug: "does-not-exist", Kind: CheckinSuccess, Now: time.Now(),
	})
	if err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRecordCheckin_SuccessSetsUpAndDeadline(t *testing.T) {
	e := newCheckinTestEnv(t)
	m := e.seedMonitor(t, "new")
	now := time.Now().UTC().Truncate(time.Second)

	res, err := e.store.RecordCheckin(context.Background(), RecordCheckinParams{
		Slug: m.Slug, Kind: CheckinSuccess, SourceIP: "203.0.113.7", UserAgent: "curl/8", Body: "ok", Now: now,
	})
	if err != nil {
		t.Fatalf("RecordCheckin: %v", err)
	}
	if res.State != "up" || res.Transition != "" {
		t.Errorf("result = %+v, want state=up transition='' (new→up is not a recovery)", res)
	}

	got := e.getMonitor(t, m.ID)
	if got.State != "up" {
		t.Errorf("state = %q, want up", got.State)
	}
	// next_deadline is the bare occurrence (late threshold): period 300s from
	// now, WITHOUT grace. The scheduler adds grace when it moves to 'late'.
	wantDeadline := now.Add(300 * time.Second)
	if !got.NextDeadline.Valid || !got.NextDeadline.Time.Equal(wantDeadline) {
		t.Errorf("next_deadline = %v, want %v", got.NextDeadline.Time, wantDeadline)
	}
	if got.FailStreak != 0 {
		t.Errorf("fail_streak = %d, want 0", got.FailStreak)
	}
	if n := e.countRows(t, `SELECT count(*) FROM checkins WHERE monitor_id=$1`, m.ID); n != 1 {
		t.Errorf("checkins = %d, want 1", n)
	}
	// new→up is not a recovery: no event/alert.
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1`, m.ID); n != 0 {
		t.Errorf("events = %d, want 0", n)
	}
}

func TestRecordCheckin_RecoveryFromDown(t *testing.T) {
	e := newCheckinTestEnv(t)
	m := e.seedMonitor(t, "down")

	res, err := e.store.RecordCheckin(context.Background(), RecordCheckinParams{
		Slug: m.Slug, Kind: CheckinSuccess, Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("RecordCheckin: %v", err)
	}
	if res.Transition != "up" {
		t.Errorf("transition = %q, want up", res.Transition)
	}
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1 AND type='up'`, m.ID); n != 1 {
		t.Errorf("up events = %d, want 1", n)
	}
	if n := e.countRows(t, `SELECT count(*) FROM alerts WHERE monitor_id=$1 AND status='pending' AND channel='default'`, m.ID); n != 1 {
		t.Errorf("pending alerts = %d, want 1", n)
	}
}

func TestRecordCheckin_FailSetsDownWithEventAndAlert(t *testing.T) {
	e := newCheckinTestEnv(t)
	m := e.seedMonitor(t, "up")

	res, err := e.store.RecordCheckin(context.Background(), RecordCheckinParams{
		Slug: m.Slug, Kind: CheckinFail, Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("RecordCheckin: %v", err)
	}
	if res.State != "down" || res.Transition != "down" {
		t.Errorf("result = %+v, want state=down transition=down", res)
	}

	got := e.getMonitor(t, m.ID)
	if got.State != "down" {
		t.Errorf("state = %q, want down", got.State)
	}
	if got.NextDeadline.Valid {
		t.Errorf("next_deadline = %v, want NULL on down", got.NextDeadline.Time)
	}
	if got.FailStreak != 1 {
		t.Errorf("fail_streak = %d, want 1", got.FailStreak)
	}
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1 AND type='down'`, m.ID); n != 1 {
		t.Errorf("down events = %d, want 1", n)
	}
	if n := e.countRows(t, `SELECT count(*) FROM alerts WHERE monitor_id=$1 AND status='pending'`, m.ID); n != 1 {
		t.Errorf("pending alerts = %d, want 1", n)
	}
}

func TestRecordCheckin_RepeatFailDoesNotDuplicateAlert(t *testing.T) {
	e := newCheckinTestEnv(t)
	m := e.seedMonitor(t, "up")
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := e.store.RecordCheckin(ctx, RecordCheckinParams{Slug: m.Slug, Kind: CheckinFail, Now: time.Now()}); err != nil {
			t.Fatalf("fail %d: %v", i, err)
		}
	}

	got := e.getMonitor(t, m.ID)
	if got.FailStreak != 3 {
		t.Errorf("fail_streak = %d, want 3 (streak still increments)", got.FailStreak)
	}
	// Only the first up→down emits an event+alert; repeats are deduped.
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1`, m.ID); n != 1 {
		t.Errorf("events = %d, want 1 (no duplicate down events)", n)
	}
	if n := e.countRows(t, `SELECT count(*) FROM alerts WHERE monitor_id=$1`, m.ID); n != 1 {
		t.Errorf("alerts = %d, want 1 (no duplicate alerts)", n)
	}
	if n := e.countRows(t, `SELECT count(*) FROM checkins WHERE monitor_id=$1`, m.ID); n != 3 {
		t.Errorf("checkins = %d, want 3 (every ping recorded)", n)
	}
}

func TestRecordCheckin_StartDoesNotChangeState(t *testing.T) {
	e := newCheckinTestEnv(t)
	m := e.seedMonitor(t, "up")
	before := e.getMonitor(t, m.ID)

	res, err := e.store.RecordCheckin(context.Background(), RecordCheckinParams{
		Slug: m.Slug, Kind: CheckinStart, Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("RecordCheckin: %v", err)
	}
	if res.Transition != "" {
		t.Errorf("transition = %q, want '' for start", res.Transition)
	}

	got := e.getMonitor(t, m.ID)
	if got.State != before.State {
		t.Errorf("state changed on start: %q → %q", before.State, got.State)
	}
	if got.NextDeadline != before.NextDeadline {
		t.Errorf("next_deadline changed on start")
	}
	if got.LastCheckinAt != before.LastCheckinAt {
		t.Errorf("last_checkin_at changed on start")
	}
	if n := e.countRows(t, `SELECT count(*) FROM checkins WHERE monitor_id=$1 AND kind='start'`, m.ID); n != 1 {
		t.Errorf("start checkins = %d, want 1", n)
	}
}

func TestRecordCheckin_AutoResumesPausedMonitor(t *testing.T) {
	e := newCheckinTestEnv(t)
	m := e.seedMonitor(t, "up")
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `UPDATE monitors SET paused_at=now() WHERE id=$1`, m.ID); err != nil {
		t.Fatalf("pause monitor: %v", err)
	}

	if _, err := e.store.RecordCheckin(ctx, RecordCheckinParams{Slug: m.Slug, Kind: CheckinSuccess, Now: time.Now()}); err != nil {
		t.Fatalf("RecordCheckin: %v", err)
	}

	got := e.getMonitor(t, m.ID)
	if got.PausedAt.Valid {
		t.Errorf("paused_at = %v, want NULL (auto-resumed)", got.PausedAt.Time)
	}
}

func TestRecordCheckin_BodyStoredVerbatimUpToCap(t *testing.T) {
	e := newCheckinTestEnv(t)
	m := e.seedMonitor(t, "up")

	// Handler truncates to 10 KB; here we pass an already-truncated body plus a
	// binary/control-char sample to confirm it round-trips (TEXT tolerates it).
	body := strings.Repeat("A", 10<<10)
	if _, err := e.store.RecordCheckin(context.Background(), RecordCheckinParams{
		Slug: m.Slug, Kind: CheckinSuccess, Body: body, Now: time.Now(),
	}); err != nil {
		t.Fatalf("RecordCheckin: %v", err)
	}

	var stored string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT body FROM checkins WHERE monitor_id=$1`, m.ID).Scan(&stored); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(stored) != len(body) {
		t.Errorf("stored body length = %d, want %d", len(stored), len(body))
	}
}

// PING-014 check-in log: raw HTML/script bodies must round-trip verbatim
// (untouched by the store) — inertness is the frontend's job (React's default
// escaping), not the database's.
func TestListCheckinsByMonitor_BodyRoundTripsVerbatim(t *testing.T) {
	e := newCheckinTestEnv(t)
	ctx := context.Background()
	m := e.seedMonitor(t, "up")

	xssBody := `<script>alert("xss")</script>`
	if _, err := e.store.RecordCheckin(ctx, RecordCheckinParams{
		Slug: m.Slug, Kind: CheckinSuccess, SourceIP: "203.0.113.9", UserAgent: "curl/8.0", Body: xssBody, Now: time.Now(),
	}); err != nil {
		t.Fatalf("RecordCheckin: %v", err)
	}

	page, err := e.store.ListCheckinsByMonitor(ctx, m.ID.String(), "", 10)
	if err != nil {
		t.Fatalf("ListCheckinsByMonitor: %v", err)
	}
	if len(page.Checkins) != 1 {
		t.Fatalf("checkins = %d, want 1", len(page.Checkins))
	}
	c := page.Checkins[0]
	if c.Body == nil || *c.Body != xssBody {
		t.Errorf("body = %v, want %q verbatim", c.Body, xssBody)
	}
	if c.SourceIP == nil || *c.SourceIP != "203.0.113.9" {
		t.Errorf("source_ip = %v, want 203.0.113.9", c.SourceIP)
	}
	if c.UserAgent == nil || *c.UserAgent != "curl/8.0" {
		t.Errorf("user_agent = %v, want curl/8.0", c.UserAgent)
	}
	if c.Kind != "success" {
		t.Errorf("kind = %q, want success", c.Kind)
	}
}

func TestListCheckinsByMonitor_CursorPaginatesNewestFirst(t *testing.T) {
	e := newCheckinTestEnv(t)
	ctx := context.Background()
	m := e.seedMonitor(t, "up")

	for i := 0; i < 6; i++ {
		if _, err := e.store.RecordCheckin(ctx, RecordCheckinParams{
			Slug: m.Slug, Kind: CheckinSuccess, Now: time.Now(),
		}); err != nil {
			t.Fatalf("RecordCheckin[%d]: %v", i, err)
		}
	}

	page1, err := e.store.ListCheckinsByMonitor(ctx, m.ID.String(), "", 4)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1.Checkins) != 4 || page1.NextCursor == "" {
		t.Fatalf("page1 = %d checkins, cursor=%q; want 4 + cursor", len(page1.Checkins), page1.NextCursor)
	}

	page2, err := e.store.ListCheckinsByMonitor(ctx, m.ID.String(), page1.NextCursor, 4)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2.Checkins) != 2 || page2.NextCursor != "" {
		t.Errorf("page2 = %d checkins, cursor=%q; want 2 + no cursor", len(page2.Checkins), page2.NextCursor)
	}
	if len(page2.Checkins) > 0 && page2.Checkins[0].ID >= page1.Checkins[len(page1.Checkins)-1].ID {
		t.Error("pages overlap; cursor pagination not strict")
	}
}
