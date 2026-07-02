package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sukhera/ping/store"
)

type fakeStore struct {
	calls atomic.Int64
	fn    func(now time.Time) (store.EvalResult, error)
}

func (f *fakeStore) EvaluateDueMonitors(_ context.Context, now time.Time, _ int32) (store.EvalResult, error) {
	f.calls.Add(1)
	if f.fn != nil {
		return f.fn(now)
	}
	return store.EvalResult{}, nil
}

// Run drives the store's evaluation each tick and stops cleanly on ctx cancel.
func TestRun_EvaluatesEachTickUntilCancelled(t *testing.T) {
	st := &fakeStore{}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- Run(ctx, st, nil, 5*time.Millisecond) }()

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

	if st.calls.Load() < 2 {
		t.Errorf("EvaluateDueMonitors called %d times, want at least 2", st.calls.Load())
	}
}
