package schedule

import (
	"errors"
	"testing"
	"time"
)

func TestDescribe(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "PRD example: daily cron with grace",
			cfg: Config{
				Kind: KindCron, CronExpr: "0 4 * * *", TZ: "Europe/Berlin", Grace: 30 * time.Minute,
			},
			want: "every day at 04:00 (Europe/Berlin); alert if 30 min late",
		},
		{
			name: "single-digit hour and minute are zero-padded",
			cfg: Config{
				Kind: KindCron, CronExpr: "5 9 * * *", TZ: "UTC", Grace: time.Minute,
			},
			want: "every day at 09:05 (UTC); alert if 1 min late",
		},
		{
			name: "simple period: minutes",
			cfg: Config{
				Kind: KindPeriod, Period: 15 * time.Minute, TZ: "UTC", Grace: 5 * time.Minute,
			},
			want: "every 15 minutes; alert if 5 min late",
		},
		{
			name: "simple period: single minute",
			cfg: Config{
				Kind: KindPeriod, Period: time.Minute, TZ: "UTC", Grace: time.Minute,
			},
			want: "every minute; alert if 1 min late",
		},
		{
			name: "simple period: single hour",
			cfg: Config{
				Kind: KindPeriod, Period: time.Hour, TZ: "UTC", Grace: time.Hour,
			},
			want: "every hour; alert if 1 hour late",
		},
		{
			name: "simple period: multiple hours",
			cfg: Config{
				Kind: KindPeriod, Period: 6 * time.Hour, TZ: "UTC", Grace: 2 * time.Hour,
			},
			want: "every 6 hours; alert if 2 hours late",
		},
		{
			name: "simple period: single day",
			cfg: Config{
				Kind: KindPeriod, Period: 24 * time.Hour, TZ: "UTC", Grace: 24 * time.Hour,
			},
			want: "every day; alert if 1 day late",
		},
		{
			name: "simple period: multiple days",
			cfg: Config{
				Kind: KindPeriod, Period: 3 * 24 * time.Hour, TZ: "UTC", Grace: 2 * 24 * time.Hour,
			},
			want: "every 3 days; alert if 2 days late",
		},
		{
			name: "cron with day-of-week restriction falls back to raw expression",
			cfg: Config{
				Kind: KindCron, CronExpr: "0 6 * * 1", TZ: "UTC", Grace: time.Minute,
			},
			want: `on schedule "0 6 * * 1" (UTC); alert if 1 min late`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Describe(tt.cfg)
			if err != nil {
				t.Fatalf("Describe() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Describe() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDescribe_InvalidConfig(t *testing.T) {
	_, err := Describe(Config{Kind: KindCron, CronExpr: "garbage", TZ: "UTC", Grace: time.Minute})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %v (%T)", err, err)
	}
}
