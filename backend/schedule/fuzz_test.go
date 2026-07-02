package schedule

import (
	"testing"
	"time"
)

// FuzzNextDeadlineCron feeds arbitrary strings as the cron expression (and a
// couple of other fields) through the full Config -> Validate -> NextDeadline
// path. The cron parser wrapper must never panic on malformed input; it
// should only ever return a *ValidationError. Per PING-006 AC.
func FuzzNextDeadlineCron(f *testing.F) {
	seeds := []string{
		"0 4 * * *",
		"*/5 * * * *",
		"0 0 1 1 *",
		"",
		"* * * * * *",
		"60 24 32 13 8",
		"not a cron expression",
		"@daily",
		"0 0 * * 1-5",
		"0,15,30,45 * * * *",
		"-- -- -- -- --",
		"0 4 * * * extra field",
		"TZ=",
		"TZ=Europe/Berlin 0 4 * * *",
		"CRON_TZ=",
		"CRON_TZ=UTC 0 4 * * *",
		"TZ",
		"=",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, expr string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("NextDeadline panicked on cron expr %q: %v", expr, r)
			}
		}()

		cfg := Config{
			Kind:     KindCron,
			CronExpr: expr,
			TZ:       "UTC",
			Grace:    time.Minute,
		}
		_, _ = NextDeadline(cfg, time.Time{}, time.Now())
		_, _ = Describe(cfg)
	})
}

// FuzzConfigValidateTZ does the same for the timezone field, which is
// resolved via time.LoadLocation.
func FuzzConfigValidateTZ(f *testing.F) {
	seeds := []string{
		"UTC",
		"Europe/Berlin",
		"",
		"Not/AZone",
		"../../../etc/passwd",
		"UTC\x00",
		"America/New_York",
		"🕐",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, tz string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Validate panicked on tz %q: %v", tz, r)
			}
		}()

		cfg := Config{Kind: KindPeriod, Period: time.Hour, TZ: tz, Grace: time.Minute}
		_ = cfg.Validate()
	})
}
