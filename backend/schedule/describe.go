package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Describe renders cfg as a human-readable sentence for the create/edit
// form's live preview (PRD F4.3), e.g.:
//
//	"every day at 04:00 (Europe/Berlin); alert if 30 min late"
//	"every 15 minutes; alert if 5 min late"
//
// It returns a ValidationError (unwrapped, as a plain error) if cfg does
// not validate — the caller decides whether to surface that as 422 detail
// or as an inline preview error.
func Describe(cfg Config) (string, error) {
	if err := cfg.Validate(); err != nil {
		return "", err
	}

	var schedulePart string
	if cfg.Kind == KindPeriod {
		schedulePart = describePeriod(cfg.Period)
	} else {
		schedulePart = describeCron(cfg.CronExpr, cfg.TZ)
	}

	return fmt.Sprintf("%s; alert if %s late", schedulePart, describeDuration(cfg.Grace)), nil
}

func describePeriod(d time.Duration) string {
	switch {
	case d == 24*time.Hour:
		return "every day"
	case d%(24*time.Hour) == 0:
		days := int(d / (24 * time.Hour))
		return fmt.Sprintf("every %d days", days)
	case d == time.Hour:
		return "every hour"
	case d%time.Hour == 0:
		hours := int(d / time.Hour)
		return fmt.Sprintf("every %d hours", hours)
	case d == time.Minute:
		return "every minute"
	default:
		minutes := int(d / time.Minute)
		return fmt.Sprintf("every %d minutes", minutes)
	}
}

// describeCron renders a small set of common cron shapes in plain language
// and falls back to the raw expression (with timezone) for anything else.
// Full natural-language cron description is out of scope for this ticket —
// the API also exposes a next-3-runs preview (PING-007) for expressions
// this doesn't special-case. Callers must pass an expr already confirmed
// parseable by cfg.Validate, so a malformed field count can't reach here.
func describeCron(expr, tz string) string {
	fields := strings.Fields(expr)
	minute, hour, dom, month, dow := fields[0], fields[1], fields[2], fields[3], fields[4]

	if dom == "*" && month == "*" && dow == "*" && isNumeric(minute) && isNumeric(hour) {
		h, _ := strconv.Atoi(hour)
		m, _ := strconv.Atoi(minute)
		return fmt.Sprintf("every day at %02d:%02d (%s)", h, m, tz)
	}

	return fmt.Sprintf("on schedule %q (%s)", expr, tz)
}

// isNumeric reports whether s is composed entirely of ASCII digits. Callers
// pass individual cron fields (never empty — strings.Fields drops empties),
// so an empty s never occurs in practice.
func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

func describeDuration(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0 && d >= 24*time.Hour:
		days := int(d / (24 * time.Hour))
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	case d%time.Hour == 0 && d >= time.Hour:
		hours := int(d / time.Hour)
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	default:
		minutes := int(d / time.Minute)
		return fmt.Sprintf("%d min", minutes)
	}
}
