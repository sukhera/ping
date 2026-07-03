package alerter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sukhera/ping/alert"
	"github.com/sukhera/ping/store"
)

// fakeStore records the outcome calls dispatch makes so tests can assert which
// terminal/retry path a job took. ClaimDueAlerts/EnqueueDownReminders are stubbed
// for the loop test; the dispatch tests call dispatch directly.
type fakeStore struct {
	jobs []store.AlertJob

	sent        []int64
	suppressed  []int64
	failed      []failCall
	rescheduled []rescheduleCall
	downtime    time.Duration
}

type failCall struct {
	id        int64
	monitorID string
	reason    string
}
type rescheduleCall struct {
	id   int64
	next time.Time
}

func (f *fakeStore) EnqueueDownReminders(context.Context, time.Time) (int, error) { return 0, nil }
func (f *fakeStore) ClaimDueAlerts(context.Context, time.Time, int32) ([]store.AlertJob, error) {
	return f.jobs, nil
}
func (f *fakeStore) MarkAlertSent(_ context.Context, id int64) error {
	f.sent = append(f.sent, id)
	return nil
}
func (f *fakeStore) SuppressAlert(_ context.Context, id int64) error {
	f.suppressed = append(f.suppressed, id)
	return nil
}
func (f *fakeStore) RescheduleAlert(_ context.Context, id int64, next time.Time) error {
	f.rescheduled = append(f.rescheduled, rescheduleCall{id, next})
	return nil
}
func (f *fakeStore) FailAlert(_ context.Context, id int64, monitorID, reason string) error {
	f.failed = append(f.failed, failCall{id, monitorID, reason})
	return nil
}
func (f *fakeStore) ResolveDowntime(context.Context, string, time.Time) (time.Duration, error) {
	return f.downtime, nil
}

// fakeChannel records the messages it was asked to send and returns a scripted
// error sequence (one entry consumed per call; the last entry repeats).
type fakeChannel struct {
	sent []alert.Message
	errs []error
}

func (c *fakeChannel) Send(_ context.Context, msg alert.Message) error {
	c.sent = append(c.sent, msg)
	if len(c.errs) == 0 {
		return nil
	}
	i := len(c.sent) - 1
	if i >= len(c.errs) {
		i = len(c.errs) - 1
	}
	return c.errs[i]
}

func downJob(id int64, attempts int) store.AlertJob {
	return store.AlertJob{
		ID:          id,
		MonitorID:   "11111111-1111-1111-1111-111111111111",
		Recipient:   "user@example.com",
		MonitorName: "nightly-backup",
		MonitorSlug: "nightly-backup",
		EventType:   "down",
		EventAt:     time.Now(),
		Attempts:    attempts,
	}
}

func TestDispatch_SuccessMarksSent(t *testing.T) {
	st := &fakeStore{}
	ch := &fakeChannel{}
	a := New(st, ch, "https://ping.example.com")

	a.dispatch(context.Background(), downJob(1, 0))

	if len(ch.sent) != 1 {
		t.Fatalf("channel Send called %d times, want 1", len(ch.sent))
	}
	if ch.sent[0].To != "user@example.com" {
		t.Errorf("To = %q, want user@example.com", ch.sent[0].To)
	}
	if len(st.sent) != 1 || st.sent[0] != 1 {
		t.Errorf("MarkAlertSent = %v, want [1]", st.sent)
	}
	if len(st.rescheduled)+len(st.failed) != 0 {
		t.Errorf("unexpected retry/fail: %+v %+v", st.rescheduled, st.failed)
	}
}

func TestDispatch_MutedSuppressesWithoutSending(t *testing.T) {
	st := &fakeStore{}
	ch := &fakeChannel{}
	a := New(st, ch, "")

	job := downJob(2, 0)
	job.AlertsMuted = true
	a.dispatch(context.Background(), job)

	if len(ch.sent) != 0 {
		t.Errorf("muted alert sent %d messages, want 0", len(ch.sent))
	}
	if len(st.suppressed) != 1 || st.suppressed[0] != 2 {
		t.Errorf("SuppressAlert = %v, want [2]", st.suppressed)
	}
}

func TestDispatch_RetryableFailureReschedulesWithBackoff(t *testing.T) {
	st := &fakeStore{}
	ch := &fakeChannel{errs: []error{&alert.SendError{Retryable: true, Op: "dial", Err: errors.New("timeout")}}}
	a := New(st, ch, "")

	before := time.Now()
	a.dispatch(context.Background(), downJob(3, 0)) // attempt 0 → retry to attempt 1

	if len(st.rescheduled) != 1 {
		t.Fatalf("RescheduleAlert called %d times, want 1", len(st.rescheduled))
	}
	delay := st.rescheduled[0].next.Sub(before)
	if delay < baseBackoff-time.Second || delay > baseBackoff+5*time.Second {
		t.Errorf("first backoff = %v, want ~%v", delay, baseBackoff)
	}
	if len(st.failed) != 0 {
		t.Errorf("unexpected fail on first retryable failure: %+v", st.failed)
	}
}

func TestDispatch_ExhaustedRetriesFail(t *testing.T) {
	st := &fakeStore{}
	ch := &fakeChannel{errs: []error{&alert.SendError{Retryable: true, Op: "dial", Err: errors.New("timeout")}}}
	a := New(st, ch, "")

	// attempts already 2 → nextAttempt 3 == maxAttempts → terminal fail.
	a.dispatch(context.Background(), downJob(4, maxAttempts-1))

	if len(st.rescheduled) != 0 {
		t.Errorf("unexpected reschedule at exhausted budget: %+v", st.rescheduled)
	}
	if len(st.failed) != 1 || st.failed[0].id != 4 {
		t.Fatalf("FailAlert = %v, want one call for id 4", st.failed)
	}
}

func TestDispatch_PermanentFailureFailsImmediately(t *testing.T) {
	st := &fakeStore{}
	ch := &fakeChannel{errs: []error{&alert.SendError{Retryable: false, Op: "rcpt", Err: errors.New("no such user")}}}
	a := New(st, ch, "")

	a.dispatch(context.Background(), downJob(5, 0))

	if len(st.rescheduled) != 0 {
		t.Errorf("permanent failure should not reschedule: %+v", st.rescheduled)
	}
	if len(st.failed) != 1 {
		t.Errorf("permanent failure should fail once, got %+v", st.failed)
	}
}

func TestDispatch_NilChannelFailsFast(t *testing.T) {
	st := &fakeStore{}
	a := New(st, nil, "")

	a.dispatch(context.Background(), downJob(6, 0))

	if len(st.failed) != 1 || st.failed[0].reason == "" {
		t.Errorf("nil channel should fail with a reason, got %+v", st.failed)
	}
}

func TestRender_RecoveryIncludesDowntime(t *testing.T) {
	st := &fakeStore{downtime: 42 * time.Minute}
	a := New(st, &fakeChannel{}, "https://ping.example.com")

	job := downJob(7, 0)
	job.EventType = "up"
	msg, err := a.render(context.Background(), job)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// PRD F3.2 subject: "[UP] <name> — recovered after 42m".
	if got := msg.Subject; got == "" || !containsAll(got, "[UP]", "42m") {
		t.Errorf("recovery subject = %q, want it to mention [UP] and 42m", got)
	}
	if got := a.dashboardURL(job.MonitorSlug); got != "https://ping.example.com/monitors/nightly-backup" {
		t.Errorf("dashboardURL = %q", got)
	}
}

func TestRender_ReminderUsesReminderTemplate(t *testing.T) {
	st := &fakeStore{downtime: 3 * time.Hour}
	a := New(st, &fakeChannel{}, "")

	// A reminder row reuses the outage's "down" event, so EventType is "down";
	// only IsReminder marks it as a reminder.
	job := downJob(8, 0)
	job.IsReminder = true
	msg, err := a.render(context.Background(), job)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// PRD F3.2: "[DOWN] <name> — still down after 3h", not the plain down subject.
	if got := msg.Subject; !containsAll(got, "still down", "3h") {
		t.Errorf("reminder subject = %q, want it to say 'still down' and '3h'", got)
	}
}

func TestBackoff_Schedule(t *testing.T) {
	want := []time.Duration{time.Minute, 5 * time.Minute, 25 * time.Minute}
	for i, w := range want {
		if got := backoff(i); got != w {
			t.Errorf("backoff(%d) = %v, want %v", i, got, w)
		}
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
