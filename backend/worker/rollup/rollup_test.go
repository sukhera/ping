package rollup

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sukhera/ping/store"
)

type fakeStore struct {
	rollupCalls atomic.Int64
	pruneCalls  atomic.Int64
	rollupErr   error
	pruneErr    error
	lastRollup  time.Time
	lastCutoff  time.Time
}

func (f *fakeStore) RollupDay(_ context.Context, day time.Time) (store.RollupResult, error) {
	f.rollupCalls.Add(1)
	f.lastRollup = day
	if f.rollupErr != nil {
		return store.RollupResult{}, f.rollupErr
	}
	return store.RollupResult{MonitorsUpdated: 1}, nil
}

func (f *fakeStore) PruneOldData(_ context.Context, cutoff time.Time) (store.PruneResult, error) {
	f.pruneCalls.Add(1)
	f.lastCutoff = cutoff
	if f.pruneErr != nil {
		return store.PruneResult{}, f.pruneErr
	}
	return store.PruneResult{}, nil
}

// TestTick_RollsUpYesterdayOnce confirms a single tick rolls up the prior UTC
// day, and a second tick the same day skips RollupDay (no double work) while
// still pruning.
func TestTick_RollsUpYesterdayOnce(t *testing.T) {
	st := &fakeStore{}
	r := New(st, 90)

	if err := r.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if st.rollupCalls.Load() != 1 {
		t.Fatalf("rollupCalls after first tick = %d, want 1", st.rollupCalls.Load())
	}
	wantYesterday := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	if !st.lastRollup.Equal(wantYesterday) {
		t.Errorf("lastRollup = %v, want %v", st.lastRollup, wantYesterday)
	}

	if err := r.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if st.rollupCalls.Load() != 1 {
		t.Errorf("rollupCalls after second same-day tick = %d, want still 1 (idempotent skip)", st.rollupCalls.Load())
	}
	if st.pruneCalls.Load() != 2 {
		t.Errorf("pruneCalls = %d, want 2 (prune runs every tick)", st.pruneCalls.Load())
	}
}

// TestTick_RollupFailureStillPrunes confirms a rollup error doesn't prevent
// pruning in the same tick — the two are independent operations.
func TestTick_RollupFailureStillPrunes(t *testing.T) {
	st := &fakeStore{rollupErr: errors.New("boom")}
	r := New(st, 90)

	if err := r.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if st.pruneCalls.Load() != 1 {
		t.Errorf("pruneCalls = %d, want 1 even though rollup failed", st.pruneCalls.Load())
	}
}

// TestTick_RollupFailureRetriesNextTick confirms a failed rollup does not
// mark the day as done, so the next tick retries it instead of silently
// skipping a day.
func TestTick_RollupFailureRetriesNextTick(t *testing.T) {
	st := &fakeStore{rollupErr: errors.New("boom")}
	r := New(st, 90)

	_ = r.tick(context.Background())
	_ = r.tick(context.Background())

	if st.rollupCalls.Load() != 2 {
		t.Errorf("rollupCalls = %d, want 2 (retry after failure)", st.rollupCalls.Load())
	}
}

// TestTick_PruneCutoffUsesRetentionDays confirms the cutoff passed to
// PruneOldData is today minus the configured retention window.
func TestTick_PruneCutoffUsesRetentionDays(t *testing.T) {
	st := &fakeStore{}
	r := New(st, 30)

	if err := r.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	wantCutoff := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -30)
	if !st.lastCutoff.Equal(wantCutoff) {
		t.Errorf("lastCutoff = %v, want %v", st.lastCutoff, wantCutoff)
	}
}

// TestNew_NonPositiveRetentionDaysDefaults confirms a zero/negative
// retentionDays (e.g. an unset config value) falls back to
// DefaultRetentionDays rather than silently retaining nothing or everything.
func TestNew_NonPositiveRetentionDaysDefaults(t *testing.T) {
	for _, days := range []int{0, -1} {
		st := &fakeStore{}
		r := New(st, days)
		if err := r.tick(context.Background()); err != nil {
			t.Fatalf("tick: %v", err)
		}
		wantCutoff := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -DefaultRetentionDays)
		if !st.lastCutoff.Equal(wantCutoff) {
			t.Errorf("retentionDays=%d: lastCutoff = %v, want %v", days, st.lastCutoff, wantCutoff)
		}
	}
}

// TestRun_TicksUntilCancelled mirrors the scheduler/alerter Run tests: the
// loop stops cleanly on ctx cancellation.
func TestRun_TicksUntilCancelled(t *testing.T) {
	st := &fakeStore{}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- Run(ctx, st, nil, 5*time.Millisecond, 90) }()

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

	if st.pruneCalls.Load() < 2 {
		t.Errorf("pruneCalls = %d, want at least 2", st.pruneCalls.Load())
	}
}
