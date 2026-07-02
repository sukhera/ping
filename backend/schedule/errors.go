package schedule

import (
	"fmt"
	"strings"
	"time"
)

// ValidationError reports that a single Config field failed validation. The
// API layer (PING-007) maps these to 422 responses with field-level detail
// per the monitors CRUD acceptance criteria.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// Validate checks that cfg is internally consistent, within the bounds
// documented in PRD F1.4 (period 1 min – 365 days, grace 1 min – 30 days),
// and that its timezone resolves and, for cron schedules, its expression
// parses. NextDeadline and Describe call Validate first and rely on both
// having already succeeded.
func (cfg Config) Validate() error {
	if cfg.TZ == "" {
		return &ValidationError{Field: "tz", Message: "timezone is required"}
	}
	if _, err := time.LoadLocation(cfg.TZ); err != nil {
		return &ValidationError{Field: "tz", Message: fmt.Sprintf("unknown timezone %q", cfg.TZ)}
	}

	if cfg.Grace < MinGrace || cfg.Grace > MaxGrace {
		return &ValidationError{Field: "grace_s", Message: fmt.Sprintf("must be between %s and %s", MinGrace, MaxGrace)}
	}

	switch cfg.Kind {
	case KindPeriod:
		if cfg.CronExpr != "" {
			return &ValidationError{Field: "cron_expr", Message: "must be empty for a period schedule"}
		}
		if cfg.Period < MinPeriod || cfg.Period > MaxPeriod {
			return &ValidationError{Field: "period_s", Message: fmt.Sprintf("must be between %s and %s", MinPeriod, MaxPeriod)}
		}
	case KindCron:
		if cfg.Period != 0 {
			return &ValidationError{Field: "period_s", Message: "must be zero for a cron schedule"}
		}
		if cfg.CronExpr == "" {
			return &ValidationError{Field: "cron_expr", Message: "cron expression is required"}
		}
		// CronExpr must be the bare 5-field expression: the monitor's
		// timezone always comes from cfg.TZ. robfig/cron/v3 also accepts an
		// embedded "TZ=..."/"CRON_TZ=..." prefix, and its parser panics on
		// a malformed one (v3.0.1 parser.go:99, no space after "TZ=" makes
		// the location slice bound negative) — reject any "=" up front so
		// that path is never reached.
		if strings.Contains(cfg.CronExpr, "=") {
			return &ValidationError{Field: "cron_expr", Message: "must not contain a timezone prefix; set tz instead"}
		}
		if len(strings.Fields(cfg.CronExpr)) != 5 {
			return &ValidationError{Field: "cron_expr", Message: "expected a 5-field cron expression"}
		}
		if err := safeParseCron(cfg.CronExpr); err != nil {
			return &ValidationError{Field: "cron_expr", Message: err.Error()}
		}
	default:
		return &ValidationError{Field: "schedule_kind", Message: fmt.Sprintf("must be %q or %q", KindPeriod, KindCron)}
	}

	return nil
}
