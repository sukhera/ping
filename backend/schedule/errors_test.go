package schedule

import (
	"errors"
	"testing"
	"time"
)

func TestValidationError_Error(t *testing.T) {
	err := &ValidationError{Field: "grace_s", Message: "must be between 1m0s and 720h0m0s"}
	want := "grace_s: must be between 1m0s and 720h0m0s"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name      string
		cfg       Config
		wantField string // empty means Validate should return nil
	}{
		{
			name: "valid period config",
			cfg:  Config{Kind: KindPeriod, Period: 15 * time.Minute, TZ: "UTC", Grace: 5 * time.Minute},
		},
		{
			name: "valid cron config",
			cfg:  Config{Kind: KindCron, CronExpr: "0 4 * * *", TZ: "Europe/Berlin", Grace: 30 * time.Minute},
		},
		{
			name: "period at minimum bound",
			cfg:  Config{Kind: KindPeriod, Period: MinPeriod, TZ: "UTC", Grace: MinGrace},
		},
		{
			name: "period at maximum bound",
			cfg:  Config{Kind: KindPeriod, Period: MaxPeriod, TZ: "UTC", Grace: MaxGrace},
		},
		{
			name:      "period below minimum",
			cfg:       Config{Kind: KindPeriod, Period: 30 * time.Second, TZ: "UTC", Grace: MinGrace},
			wantField: "period_s",
		},
		{
			name:      "period above maximum",
			cfg:       Config{Kind: KindPeriod, Period: MaxPeriod + time.Hour, TZ: "UTC", Grace: MinGrace},
			wantField: "period_s",
		},
		{
			name:      "grace below minimum",
			cfg:       Config{Kind: KindPeriod, Period: MinPeriod, TZ: "UTC", Grace: 30 * time.Second},
			wantField: "grace_s",
		},
		{
			name:      "grace above maximum",
			cfg:       Config{Kind: KindPeriod, Period: MinPeriod, TZ: "UTC", Grace: MaxGrace + time.Hour},
			wantField: "grace_s",
		},
		{
			name:      "missing timezone",
			cfg:       Config{Kind: KindPeriod, Period: MinPeriod, TZ: "", Grace: MinGrace},
			wantField: "tz",
		},
		{
			name:      "unknown timezone",
			cfg:       Config{Kind: KindPeriod, Period: MinPeriod, TZ: "Mars/OlympusMons", Grace: MinGrace},
			wantField: "tz",
		},
		{
			name:      "missing cron expression",
			cfg:       Config{Kind: KindCron, CronExpr: "", TZ: "UTC", Grace: MinGrace},
			wantField: "cron_expr",
		},
		{
			name:      "malformed cron expression",
			cfg:       Config{Kind: KindCron, CronExpr: "not a cron expr at all", TZ: "UTC", Grace: MinGrace},
			wantField: "cron_expr",
		},
		{
			name:      "too few cron fields",
			cfg:       Config{Kind: KindCron, CronExpr: "0 4 * *", TZ: "UTC", Grace: MinGrace},
			wantField: "cron_expr",
		},
		{
			// robfig/cron/v3 accepts an embedded "TZ=..."/"CRON_TZ=..."
			// prefix and panics on a malformed one (v3.0.1 parser.go:99).
			// The monitor's timezone always comes from cfg.TZ, so any "="
			// in CronExpr must be rejected before it reaches the parser.
			name:      "cron expression with malformed TZ= prefix is rejected before parsing",
			cfg:       Config{Kind: KindCron, CronExpr: "TZ=", TZ: "UTC", Grace: MinGrace},
			wantField: "cron_expr",
		},
		{
			name:      "cron expression with well-formed TZ= prefix is still rejected",
			cfg:       Config{Kind: KindCron, CronExpr: "TZ=Europe/Berlin 0 4 * * *", TZ: "UTC", Grace: MinGrace},
			wantField: "cron_expr",
		},
		{
			name:      "cron config carrying a period value",
			cfg:       Config{Kind: KindCron, CronExpr: "0 4 * * *", Period: time.Hour, TZ: "UTC", Grace: MinGrace},
			wantField: "period_s",
		},
		{
			name:      "period config carrying a cron expression",
			cfg:       Config{Kind: KindPeriod, Period: time.Hour, CronExpr: "0 4 * * *", TZ: "UTC", Grace: MinGrace},
			wantField: "cron_expr",
		},
		{
			name:      "unknown schedule kind",
			cfg:       Config{Kind: Kind("weekly-ish"), TZ: "UTC", Grace: MinGrace},
			wantField: "schedule_kind",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantField == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("Validate() error = %v (%T), want *ValidationError", err, err)
			}
			if ve.Field != tt.wantField {
				t.Errorf("Field = %q, want %q", ve.Field, tt.wantField)
			}
		})
	}
}
