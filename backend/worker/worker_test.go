package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRun_TicksUntilContextCancelled(t *testing.T) {
	var ticks atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Name:     "test",
			Interval: 5 * time.Millisecond,
			Tick: func(context.Context) error {
				ticks.Add(1)
				return nil
			},
		})
	}()

	// Let several ticks happen, then stop.
	time.Sleep(60 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil on graceful stop", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancel")
	}

	if got := ticks.Load(); got < 2 {
		t.Errorf("ticks = %d, want at least 2", got)
	}
}

func TestRun_TickErrorDoesNotStopLoop(t *testing.T) {
	var ticks atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = Run(ctx, Config{
			Name:     "test",
			Interval: 5 * time.Millisecond,
			Tick: func(context.Context) error {
				if ticks.Add(1) >= 5 {
					close(done)
				}
				return errors.New("boom") // every tick errors; loop must continue
			},
		})
	}()

	select {
	case <-done:
		// Reached 5 ticks despite every tick erroring — loop survived.
	case <-time.After(time.Second):
		t.Fatalf("loop stopped early after %d ticks", ticks.Load())
	}
}

func TestRun_FiresImmediately(t *testing.T) {
	ticked := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = Run(ctx, Config{
			Name:     "test",
			Interval: time.Hour, // long: only the immediate first tick should fire
			Tick: func(context.Context) error {
				ticked <- struct{}{}
				return nil
			},
		})
	}()

	select {
	case <-ticked:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first tick did not fire immediately")
	}
}

func TestRun_NilHeartbeatIsNoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	// HB is nil — Run must not panic writing the heartbeat.
	err := Run(ctx, Config{
		Name:     "test",
		Interval: 5 * time.Millisecond,
		Tick:     func(context.Context) error { return nil },
		HB:       nil,
	})
	if err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
}

func TestJitter(t *testing.T) {
	if got := jitter(0); got != 0 {
		t.Errorf("jitter(0) = %v, want 0", got)
	}
	for range 100 {
		if got := jitter(10 * time.Millisecond); got < 0 || got >= 10*time.Millisecond {
			t.Fatalf("jitter out of [0,10ms): %v", got)
		}
	}
}
