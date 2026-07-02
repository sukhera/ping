package schedule

import (
	"errors"
	"testing"
	"time"
)

func mustParse(t *testing.T, layout, value string) time.Time {
	t.Helper()
	tm, err := time.Parse(layout, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return tm
}

func inLoc(t *testing.T, tz, layout, value string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatalf("load location %q: %v", tz, err)
	}
	tm, err := time.ParseInLocation(layout, value, loc)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return tm
}

const rfc3339 = "2006-01-02T15:04:05Z07:00"

func TestNextDeadline_Period(t *testing.T) {
	tests := []struct {
		name         string
		cfg          Config
		lastCheckin  time.Time
		now          time.Time
		wantDeadline time.Time
	}{
		{
			name: "simple period from last checkin",
			cfg: Config{
				Kind: KindPeriod, Period: 15 * time.Minute, TZ: "UTC", Grace: 5 * time.Minute,
			},
			lastCheckin:  mustParse(t, rfc3339, "2026-01-01T00:00:00Z"),
			now:          mustParse(t, rfc3339, "2026-01-01T00:01:00Z"),
			wantDeadline: mustParse(t, rfc3339, "2026-01-01T00:20:00Z"),
		},
		{
			name: "no prior checkin falls back to now",
			cfg: Config{
				Kind: KindPeriod, Period: time.Hour, TZ: "UTC", Grace: 10 * time.Minute,
			},
			lastCheckin:  time.Time{},
			now:          mustParse(t, rfc3339, "2026-01-01T00:00:00Z"),
			wantDeadline: mustParse(t, rfc3339, "2026-01-01T01:10:00Z"),
		},
		{
			name: "daily period across a month boundary",
			cfg: Config{
				Kind: KindPeriod, Period: 24 * time.Hour, TZ: "UTC", Grace: time.Minute,
			},
			lastCheckin:  mustParse(t, rfc3339, "2026-01-31T12:00:00Z"),
			now:          mustParse(t, rfc3339, "2026-01-31T12:05:00Z"),
			wantDeadline: mustParse(t, rfc3339, "2026-02-01T12:01:00Z"),
		},
		{
			name: "daily period across a year boundary",
			cfg: Config{
				Kind: KindPeriod, Period: 24 * time.Hour, TZ: "UTC", Grace: MinGrace,
			},
			lastCheckin:  mustParse(t, rfc3339, "2026-12-31T00:00:00Z"),
			now:          mustParse(t, rfc3339, "2026-12-31T00:00:01Z"),
			wantDeadline: mustParse(t, rfc3339, "2027-01-01T00:01:00Z"),
		},
		{
			name: "365-day max period",
			cfg: Config{
				Kind: KindPeriod, Period: MaxPeriod, TZ: "UTC", Grace: MinGrace,
			},
			lastCheckin:  mustParse(t, rfc3339, "2026-01-01T00:00:00Z"),
			now:          mustParse(t, rfc3339, "2026-01-01T00:00:00Z"),
			wantDeadline: mustParse(t, rfc3339, "2027-01-01T00:01:00Z"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NextDeadline(tt.cfg, tt.lastCheckin, tt.now)
			if err != nil {
				t.Fatalf("NextDeadline() error = %v", err)
			}
			if !got.Equal(tt.wantDeadline) {
				t.Errorf("NextDeadline() = %v, want %v", got, tt.wantDeadline)
			}
		})
	}
}

func TestNextDeadline_Cron(t *testing.T) {
	tests := []struct {
		name         string
		cfg          Config
		lastCheckin  time.Time
		now          time.Time
		wantDeadline time.Time
	}{
		{
			name: "daily at 04:00 Berlin, normal day",
			cfg: Config{
				Kind: KindCron, CronExpr: "0 4 * * *", TZ: "Europe/Berlin", Grace: 30 * time.Minute,
			},
			lastCheckin:  inLoc(t, "Europe/Berlin", rfc3339, "2026-01-01T04:00:00+01:00"),
			now:          inLoc(t, "Europe/Berlin", rfc3339, "2026-01-01T04:05:00+01:00"),
			wantDeadline: inLoc(t, "Europe/Berlin", rfc3339, "2026-01-02T04:30:00+01:00"),
		},
		{
			// 2026-03-29: Berlin clocks jump from 02:00 to 03:00 CEST.
			// A 02:30 cron job has no literal occurrence that day; cron's
			// wall-clock arithmetic resolves it to the next valid instant.
			name: "cron time inside the spring-forward gap",
			cfg: Config{
				Kind: KindCron, CronExpr: "30 2 * * *", TZ: "Europe/Berlin", Grace: MinGrace,
			},
			lastCheckin: inLoc(t, "Europe/Berlin", rfc3339, "2026-03-28T02:30:00+01:00"),
			now:         inLoc(t, "Europe/Berlin", rfc3339, "2026-03-28T02:31:00+01:00"),
			// robfig/cron walks forward minute-by-minute in wall-clock terms;
			// 02:30 on the 29th doesn't exist, so it lands on the 30th at 02:30 CEST.
			wantDeadline: inLoc(t, "Europe/Berlin", rfc3339, "2026-03-30T02:31:00+02:00"),
		},
		{
			name: "cron time crossing spring-forward, later time of day",
			cfg: Config{
				Kind: KindCron, CronExpr: "0 4 * * *", TZ: "Europe/Berlin", Grace: MinGrace,
			},
			lastCheckin:  inLoc(t, "Europe/Berlin", rfc3339, "2026-03-28T04:00:00+01:00"),
			now:          inLoc(t, "Europe/Berlin", rfc3339, "2026-03-28T04:05:00+01:00"),
			wantDeadline: inLoc(t, "Europe/Berlin", rfc3339, "2026-03-29T04:01:00+02:00"),
		},
		{
			// 2026-10-25: Berlin clocks fall back from 03:00 CEST to 02:00 CET;
			// 02:30 occurs twice. Go's time package resolves the ambiguous
			// wall-clock time to the first occurrence (pre-transition, CEST).
			name: "cron time inside the fall-back repeated hour",
			cfg: Config{
				Kind: KindCron, CronExpr: "30 2 * * *", TZ: "Europe/Berlin", Grace: MinGrace,
			},
			lastCheckin:  inLoc(t, "Europe/Berlin", rfc3339, "2026-10-24T02:30:00+02:00"),
			now:          inLoc(t, "Europe/Berlin", rfc3339, "2026-10-24T02:31:00+02:00"),
			wantDeadline: inLoc(t, "Europe/Berlin", rfc3339, "2026-10-25T02:31:00+02:00"),
		},
		{
			name: "cron time crossing fall-back, later time of day",
			cfg: Config{
				Kind: KindCron, CronExpr: "0 4 * * *", TZ: "Europe/Berlin", Grace: MinGrace,
			},
			lastCheckin:  inLoc(t, "Europe/Berlin", rfc3339, "2026-10-24T04:00:00+02:00"),
			now:          inLoc(t, "Europe/Berlin", rfc3339, "2026-10-24T04:05:00+02:00"),
			wantDeadline: inLoc(t, "Europe/Berlin", rfc3339, "2026-10-25T04:01:00+01:00"),
		},
		{
			// US DST rules differ from EU (second Sunday March). Confirms TZ
			// handling isn't hardcoded to Europe/Berlin's transition dates.
			name: "America/New_York spring-forward (2026-03-08)",
			cfg: Config{
				Kind: KindCron, CronExpr: "0 3 * * *", TZ: "America/New_York", Grace: MinGrace,
			},
			lastCheckin:  inLoc(t, "America/New_York", rfc3339, "2026-03-07T03:00:00-05:00"),
			now:          inLoc(t, "America/New_York", rfc3339, "2026-03-07T03:05:00-05:00"),
			wantDeadline: inLoc(t, "America/New_York", rfc3339, "2026-03-08T03:01:00-04:00"),
		},
		{
			name: "month boundary, last day of Jan to Feb",
			cfg: Config{
				Kind: KindCron, CronExpr: "0 0 * * *", TZ: "UTC", Grace: MinGrace,
			},
			lastCheckin:  mustParse(t, rfc3339, "2026-01-31T00:00:00Z"),
			now:          mustParse(t, rfc3339, "2026-01-31T00:05:00Z"),
			wantDeadline: mustParse(t, rfc3339, "2026-02-01T00:01:00Z"),
		},
		{
			name: "year boundary, Dec 31 to Jan 1",
			cfg: Config{
				Kind: KindCron, CronExpr: "0 0 * * *", TZ: "UTC", Grace: MinGrace,
			},
			lastCheckin:  mustParse(t, rfc3339, "2026-12-31T00:00:00Z"),
			now:          mustParse(t, rfc3339, "2026-12-31T00:05:00Z"),
			wantDeadline: mustParse(t, rfc3339, "2027-01-01T00:01:00Z"),
		},
		{
			name: "monitor TZ differs from server TZ (evaluated in UTC 'now')",
			cfg: Config{
				Kind: KindCron, CronExpr: "0 9 * * *", TZ: "Asia/Tokyo", Grace: MinGrace,
			},
			// 09:00 JST == 00:00 UTC
			lastCheckin:  mustParse(t, rfc3339, "2026-06-01T00:00:00Z"),
			now:          mustParse(t, rfc3339, "2026-06-01T00:05:00Z"),
			wantDeadline: inLoc(t, "Asia/Tokyo", rfc3339, "2026-06-02T09:01:00+09:00"),
		},
		{
			name: "day-of-week cron: every Monday",
			cfg: Config{
				Kind: KindCron, CronExpr: "0 6 * * 1", TZ: "UTC", Grace: MinGrace,
			},
			lastCheckin:  mustParse(t, rfc3339, "2026-01-05T06:00:00Z"), // a Monday
			now:          mustParse(t, rfc3339, "2026-01-05T06:05:00Z"),
			wantDeadline: mustParse(t, rfc3339, "2026-01-12T06:01:00Z"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NextDeadline(tt.cfg, tt.lastCheckin, tt.now)
			if err != nil {
				t.Fatalf("NextDeadline() error = %v", err)
			}
			if !got.Equal(tt.wantDeadline) {
				t.Errorf("NextDeadline() = %v, want %v", got, tt.wantDeadline)
			}
		})
	}
}

func TestNextDeadline_InvalidConfig(t *testing.T) {
	_, err := NextDeadline(Config{Kind: KindCron, CronExpr: "not a cron", TZ: "UTC", Grace: time.Minute}, time.Time{}, time.Now())
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %v (%T)", err, err)
	}
	if ve.Field != "cron_expr" {
		t.Errorf("Field = %q, want %q", ve.Field, "cron_expr")
	}
}

func TestNextDeadline_UnknownTZ(t *testing.T) {
	_, err := NextDeadline(Config{Kind: KindCron, CronExpr: "0 4 * * *", TZ: "Not/AZone", Grace: time.Minute}, time.Time{}, time.Now())
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %v (%T)", err, err)
	}
	if ve.Field != "tz" {
		t.Errorf("Field = %q, want %q", ve.Field, "tz")
	}
}
