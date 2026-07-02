//go:build integration

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sukhera/ping/db"
)

// seedOwnedMonitor creates a user + heartbeat monitor (period_s=300, grace_s=60)
// and returns the monitor plus the owner's user id string, for pause/resume/mute
// tests that call the user-scoped store methods.
func (e *checkinTestEnv) seedOwnedMonitor(t *testing.T, autoResume bool) (db.Monitor, string) {
	t.Helper()
	ctx := context.Background()

	email := "events+" + strings.ReplaceAll(t.Name(), "/", "_") + time.Now().Format("150405.000000000") + "@example.test"
	user, err := e.q.CreateUser(ctx, db.CreateUserParams{Email: email, PasswordHash: "x"})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	m, err := e.q.CreateMonitor(ctx, db.CreateMonitorParams{
		UserID:       user.ID,
		Kind:         "heartbeat",
		Slug:         "ev-" + time.Now().Format("150405.000000000"),
		Name:         "events monitor",
		ScheduleKind: pgtype.Text{String: "period", Valid: true},
		PeriodS:      pgtype.Int4{Int32: 300, Valid: true},
		GraceS:       pgtype.Int4{Int32: 60, Valid: true},
		Tz:           "UTC",
		AutoResume:   pgtype.Bool{Bool: autoResume, Valid: true},
	})
	if err != nil {
		t.Fatalf("seed monitor: %v", err)
	}
	return m, user.ID.String()
}

func TestPauseMonitor_SetsFlagAndEventLeavesStateUntouched(t *testing.T) {
	e := newCheckinTestEnv(t)
	ctx := context.Background()
	m, userID := e.seedOwnedMonitor(t, true)
	// Put it in a definite state to prove pause leaves `state` alone.
	if _, err := e.pool.Exec(ctx, `UPDATE monitors SET state='up' WHERE id=$1`, m.ID); err != nil {
		t.Fatalf("set state: %v", err)
	}

	out, err := e.store.PauseMonitor(ctx, m.ID.String(), userID)
	if err != nil {
		t.Fatalf("PauseMonitor: %v", err)
	}
	if out.PausedAt == nil {
		t.Error("paused_at not set")
	}
	if out.State != "up" {
		t.Errorf("state = %q, want up (untouched by pause)", out.State)
	}
	if out.DisplayState != "paused" {
		t.Errorf("display_state = %q, want paused", out.DisplayState)
	}
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1 AND type='pause'`, m.ID); n != 1 {
		t.Errorf("pause events = %d, want 1", n)
	}
}

// AC-1 companion: a paused monitor is excluded from the scheduler claim, so it
// never goes late/down while paused.
func TestPausedMonitor_NotClaimedByScheduler(t *testing.T) {
	e := newCheckinTestEnv(t)
	ctx := context.Background()
	m, userID := e.seedOwnedMonitor(t, true)
	// Make it due (deadline in the past) and up, then pause it.
	past := time.Now().UTC().Add(-time.Hour)
	if _, err := e.pool.Exec(ctx, `UPDATE monitors SET state='up', next_deadline=$1 WHERE id=$2`, past, m.ID); err != nil {
		t.Fatalf("arm: %v", err)
	}
	if _, err := e.store.PauseMonitor(ctx, m.ID.String(), userID); err != nil {
		t.Fatalf("pause: %v", err)
	}

	res, err := e.store.EvaluateDueMonitors(ctx, time.Now().UTC(), 100)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	// The paused, past-due monitor must not have been transitioned.
	got := e.getMonitor(t, m.ID)
	if got.State != "up" {
		t.Errorf("paused monitor transitioned to %q; scheduler should skip it", got.State)
	}
	_ = res
}

// AC-2: a monitor paused past its deadline does NOT go late/down immediately on
// resume — resume re-arms next_deadline from now and sets state=up.
func TestResumeMonitor_ReArmsDeadlineNoImmediateTrip(t *testing.T) {
	e := newCheckinTestEnv(t)
	ctx := context.Background()
	m, userID := e.seedOwnedMonitor(t, true)
	// Paused while down, with a long-past deadline.
	past := time.Now().UTC().Add(-24 * time.Hour)
	if _, err := e.pool.Exec(ctx,
		`UPDATE monitors SET state='down', next_deadline=$1, paused_at=now() WHERE id=$2`, past, m.ID); err != nil {
		t.Fatalf("arm: %v", err)
	}

	now := time.Now().UTC()
	out, err := e.store.ResumeMonitor(ctx, m.ID.String(), userID, now)
	if err != nil {
		t.Fatalf("ResumeMonitor: %v", err)
	}
	if out.PausedAt != nil {
		t.Error("paused_at still set after resume")
	}
	if out.State != "up" {
		t.Errorf("state = %q, want up after resume", out.State)
	}
	// next_deadline re-armed to now + period (300s), safely in the future.
	if out.NextDeadline == nil || !out.NextDeadline.After(now) {
		t.Errorf("next_deadline = %v, want future (re-armed from resume)", out.NextDeadline)
	}

	// The immediate next scheduler tick must NOT claim it (deadline is future).
	res, err := e.store.EvaluateDueMonitors(ctx, now.Add(time.Second), 100)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if res.ToLate != 0 || res.ToDown != 0 {
		t.Errorf("resumed monitor tripped immediately: %+v", res)
	}
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1 AND type='resume'`, m.ID); n != 1 {
		t.Errorf("resume events = %d, want 1", n)
	}
}

// auto_resume=false: a successful check-in records and re-arms, but leaves the
// monitor paused (paused_at stays set).
func TestCheckin_AutoResumeFalseKeepsPaused(t *testing.T) {
	e := newCheckinTestEnv(t)
	ctx := context.Background()
	m, _ := e.seedOwnedMonitor(t, false) // auto_resume = false
	if _, err := e.pool.Exec(ctx, `UPDATE monitors SET paused_at=now() WHERE id=$1`, m.ID); err != nil {
		t.Fatalf("pause: %v", err)
	}

	if _, err := e.store.RecordCheckin(ctx, RecordCheckinParams{
		Slug: m.Slug, Kind: CheckinSuccess, Now: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordCheckin: %v", err)
	}

	got := e.getMonitor(t, m.ID)
	if !got.PausedAt.Valid {
		t.Error("paused_at cleared despite auto_resume=false")
	}
	if got.State != "up" {
		t.Errorf("state = %q, want up (checkin still records)", got.State)
	}
}

// auto_resume=true (default): a check-in clears paused_at (existing PING-008
// behaviour, now gated on the flag).
func TestCheckin_AutoResumeTrueClearsPaused(t *testing.T) {
	e := newCheckinTestEnv(t)
	ctx := context.Background()
	m, _ := e.seedOwnedMonitor(t, true)
	if _, err := e.pool.Exec(ctx, `UPDATE monitors SET paused_at=now() WHERE id=$1`, m.ID); err != nil {
		t.Fatalf("pause: %v", err)
	}

	if _, err := e.store.RecordCheckin(ctx, RecordCheckinParams{
		Slug: m.Slug, Kind: CheckinSuccess, Now: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordCheckin: %v", err)
	}

	got := e.getMonitor(t, m.ID)
	if got.PausedAt.Valid {
		t.Error("paused_at not cleared despite auto_resume=true")
	}
}

func TestMuteUnmute_TogglesFlagWithEvents(t *testing.T) {
	e := newCheckinTestEnv(t)
	ctx := context.Background()
	m, userID := e.seedOwnedMonitor(t, true)

	out, err := e.store.MuteMonitor(ctx, m.ID.String(), userID)
	if err != nil {
		t.Fatalf("MuteMonitor: %v", err)
	}
	if !out.AlertsMuted {
		t.Error("alerts_muted not set")
	}

	out, err = e.store.UnmuteMonitor(ctx, m.ID.String(), userID)
	if err != nil {
		t.Fatalf("UnmuteMonitor: %v", err)
	}
	if out.AlertsMuted {
		t.Error("alerts_muted still set after unmute")
	}
	if n := e.countRows(t, `SELECT count(*) FROM events WHERE monitor_id=$1 AND type IN ('mute','unmute')`, m.ID); n != 2 {
		t.Errorf("mute/unmute events = %d, want 2", n)
	}
}

func TestFlagMonitor_ForeignOrMissingReturnsNotFound(t *testing.T) {
	e := newCheckinTestEnv(t)
	ctx := context.Background()
	m, _ := e.seedOwnedMonitor(t, true)
	_, otherUserID := e.seedOwnedMonitor(t, true) // a different user

	// User B pausing user A's monitor → ErrNotFound (the id+user_id WHERE
	// matches nothing; the handler's GetMonitor is what yields 403 upstream).
	if _, err := e.store.PauseMonitor(ctx, m.ID.String(), otherUserID); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign pause err = %v, want ErrNotFound", err)
	}
}

// AC-3 + AC-4: every event type appears in the feed and the feed filters by
// monitor and type.
func TestEventFeed_ShowsAllTypesAndFilters(t *testing.T) {
	e := newCheckinTestEnv(t)
	ctx := context.Background()
	m, userID := e.seedOwnedMonitor(t, true)

	// Generate a config_change, pause, resume, mute, unmute — five event types.
	if _, err := e.store.UpdateMonitor(ctx, m.ID.String(), userID, UpdateMonitorParams{Name: "renamed"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := e.store.PauseMonitor(ctx, m.ID.String(), userID); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := e.store.ResumeMonitor(ctx, m.ID.String(), userID, time.Now().UTC()); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if _, err := e.store.MuteMonitor(ctx, m.ID.String(), userID); err != nil {
		t.Fatalf("mute: %v", err)
	}
	if _, err := e.store.UnmuteMonitor(ctx, m.ID.String(), userID); err != nil {
		t.Fatalf("unmute: %v", err)
	}

	// Per-monitor feed: all five newest-first.
	page, err := e.store.ListEventsByMonitor(ctx, m.ID.String(), "", "", 50)
	if err != nil {
		t.Fatalf("ListEventsByMonitor: %v", err)
	}
	types := map[string]bool{}
	for _, ev := range page.Events {
		types[ev.Type] = true
	}
	for _, want := range []string{"config_change", "pause", "resume", "mute", "unmute"} {
		if !types[want] {
			t.Errorf("feed missing event type %q", want)
		}
	}
	// Newest-first ordering (unmute was last).
	if len(page.Events) > 0 && page.Events[0].Type != "unmute" {
		t.Errorf("first event = %q, want unmute (newest first)", page.Events[0].Type)
	}

	// Type filter (global feed).
	filtered, err := e.store.ListEventsByUser(ctx, userID, "", "pause", "", 50)
	if err != nil {
		t.Fatalf("ListEventsByUser(type=pause): %v", err)
	}
	if len(filtered.Events) != 1 || filtered.Events[0].Type != "pause" {
		t.Errorf("type-filtered feed = %+v, want exactly one pause", filtered.Events)
	}

	// Monitor filter (global feed) returns only this monitor's events.
	byMon, err := e.store.ListEventsByUser(ctx, userID, m.ID.String(), "", "", 50)
	if err != nil {
		t.Fatalf("ListEventsByUser(monitor): %v", err)
	}
	if len(byMon.Events) != 5 {
		t.Errorf("monitor-filtered feed = %d events, want 5", len(byMon.Events))
	}
}

func TestEventFeed_CursorPaginates(t *testing.T) {
	e := newCheckinTestEnv(t)
	ctx := context.Background()
	m, userID := e.seedOwnedMonitor(t, true)

	// Six events via mute/unmute toggles.
	for i := 0; i < 3; i++ {
		if _, err := e.store.MuteMonitor(ctx, m.ID.String(), userID); err != nil {
			t.Fatalf("mute: %v", err)
		}
		if _, err := e.store.UnmuteMonitor(ctx, m.ID.String(), userID); err != nil {
			t.Fatalf("unmute: %v", err)
		}
	}

	page1, err := e.store.ListEventsByMonitor(ctx, m.ID.String(), "", "", 4)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1.Events) != 4 || page1.NextCursor == "" {
		t.Fatalf("page1 = %d events, cursor=%q; want 4 + cursor", len(page1.Events), page1.NextCursor)
	}

	page2, err := e.store.ListEventsByMonitor(ctx, m.ID.String(), "", page1.NextCursor, 4)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2.Events) != 2 || page2.NextCursor != "" {
		t.Errorf("page2 = %d events, cursor=%q; want 2 + no cursor", len(page2.Events), page2.NextCursor)
	}
	// No overlap: page2's first id must be < page1's last id.
	if len(page2.Events) > 0 && page2.Events[0].ID >= page1.Events[len(page1.Events)-1].ID {
		t.Error("pages overlap; cursor pagination not strict")
	}
}
