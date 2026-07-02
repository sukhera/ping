// Package schedule implements pure period/cron/grace math for heartbeat
// monitors: computing the next deadline a check-in is due by, and rendering
// a schedule config as a human-readable description. It performs zero I/O
// and is safe to call from both the API (validation, live preview) and the
// scheduler worker (deadline evaluation).
package schedule

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// Kind identifies which schedule variant a Config uses.
type Kind string

const (
	KindPeriod Kind = "period"
	KindCron   Kind = "cron"
)

const (
	MinPeriod = time.Minute
	MaxPeriod = 365 * 24 * time.Hour

	MinGrace = time.Minute
	MaxGrace = 30 * 24 * time.Hour
)

// cronParser is the standard 5-field cron parser (minute hour dom month dow),
// matching PRD F1.4's "standard 5-field syntax".
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// safeParseCron parses expr and converts any panic from the underlying
// robfig/cron/v3 parser (an unmaintained dependency with at least one known
// panic on malformed input, see errors.go) into a plain error, so no
// caller-supplied string can crash the process.
func safeParseCron(expr string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("invalid cron expression: %v", r)
		}
	}()
	_, err = cronParser.Parse(expr)
	return err
}

// Config is the schedule portion of a heartbeat monitor: either a simple
// period or a cron expression, always paired with an IANA timezone and a
// grace period. It mirrors the schedule_kind/period_s/cron_expr/tz/grace_s
// columns on the monitors table.
type Config struct {
	Kind     Kind
	Period   time.Duration // used when Kind == KindPeriod
	CronExpr string        // used when Kind == KindCron
	TZ       string        // IANA timezone, e.g. "Europe/Berlin"
	Grace    time.Duration
}

// NextDeadline returns the instant by which the next check-in must arrive,
// given the last known check-in time (zero value if none yet) and the
// current time. The deadline is the next scheduled occurrence after
// lastCheckin (or now, if there is no prior check-in) plus the grace period.
//
// For period schedules the next occurrence is simply lastCheckin + Period
// (or now + Period when there is no prior check-in). For cron schedules the
// next occurrence is computed in the monitor's timezone via robfig/cron,
// which is DST-correct: a spring-forward gap is skipped forward past, and a
// fall-back repeat resolves to the first occurrence, exactly as the time
// package's Location-aware arithmetic behaves.
func NextDeadline(cfg Config, lastCheckin, now time.Time) (time.Time, error) {
	if err := cfg.Validate(); err != nil {
		return time.Time{}, err
	}

	from := lastCheckin
	if from.IsZero() {
		from = now
	}

	// cfg.Validate has already confirmed TZ resolves and, for cron
	// schedules, that CronExpr parses — so both are re-derived here
	// without further error handling.
	if cfg.Kind == KindPeriod {
		return from.Add(cfg.Period).Add(cfg.Grace), nil
	}

	loc, _ := time.LoadLocation(cfg.TZ)
	sched, _ := cronParser.Parse(cfg.CronExpr)
	return sched.Next(from.In(loc)).Add(cfg.Grace), nil
}
