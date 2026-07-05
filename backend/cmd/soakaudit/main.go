// Command soakaudit checks the three PING-023 soak-test invariants against a
// live database at the end of a soak run (hack/soak/run.sh): every down
// transition got exactly one non-reminder alert row that eventually resolved,
// no monitor is stuck in "late" past its grace period, and no monitor has two
// consecutive same-type transitions with no opposite-type event between them.
// It connects directly with pgx rather than through backend/store, since
// these are one-off aggregate audit queries, not part of the app's normal
// read/write path.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dsn := flag.String("database-url", os.Getenv("DATABASE_URL"), "Postgres connection string (defaults to $DATABASE_URL)")
	pendingGrace := flag.Duration("pending-grace", 5*time.Minute, "how recent a down event's alert may still be legitimately 'pending' (alerter tick lag), not a violation")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	if *dsn == "" {
		slog.Error("fatal", "error", "no DATABASE_URL and -database-url not set")
		os.Exit(1)
	}

	ok, err := run(context.Background(), *dsn, *pendingGrace)
	if err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
	if !ok {
		os.Exit(1)
	}
}

func run(ctx context.Context, dsn string, pendingGrace time.Duration) (bool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return false, fmt.Errorf("run: connect postgres: %w", err)
	}
	defer pool.Close()

	summary, err := fetchSummary(ctx, pool)
	if err != nil {
		return false, err
	}
	fmt.Printf("monitors=%d checkins=%d probe_results=%d up_down_events=%d alerts=%d\n",
		summary.Monitors, summary.Checkins, summary.ProbeResults, summary.UpDownEvents, summary.Alerts)

	missingOrDup, err := checkAlertPerDownEvent(ctx, pool, pendingGrace)
	if err != nil {
		return false, err
	}
	stuckLate, err := checkNoStuckLate(ctx, pool)
	if err != nil {
		return false, err
	}
	dupTransitions, err := checkNoDuplicateTransitions(ctx, pool)
	if err != nil {
		return false, err
	}

	ok := len(missingOrDup) == 0 && len(stuckLate) == 0 && len(dupTransitions) == 0

	report(missingOrDup, stuckLate, dupTransitions)
	if ok {
		fmt.Println("PASS: all invariants held")
	} else {
		fmt.Println("FAIL: invariant violations found (see above)")
	}
	return ok, nil
}

type runSummary struct {
	Monitors     int64
	Checkins     int64
	ProbeResults int64
	UpDownEvents int64
	Alerts       int64
}

func fetchSummary(ctx context.Context, pool *pgxpool.Pool) (runSummary, error) {
	var s runSummary
	err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM monitors),
			(SELECT count(*) FROM checkins),
			(SELECT count(*) FROM probe_results),
			(SELECT count(*) FROM events WHERE type IN ('up', 'down')),
			(SELECT count(*) FROM alerts)
	`).Scan(&s.Monitors, &s.Checkins, &s.ProbeResults, &s.UpDownEvents, &s.Alerts)
	if err != nil {
		return runSummary{}, fmt.Errorf("fetchSummary: %w", err)
	}
	return s, nil
}

// alertViolation is one down event that failed the "exactly one non-reminder
// alert, eventually resolved" invariant.
type alertViolation struct {
	EventID        int64
	MonitorID      string
	NonReminders   int64
	StillPending   int64
	EventCreatedAt time.Time
}

// checkAlertPerDownEvent finds down events with != 1 non-reminder alert row,
// or whose non-reminder alert is still 'pending' after pendingGrace has
// elapsed since the event (a fresh down event's alert may legitimately still
// be pending — the alerter simply hasn't ticked yet).
func checkAlertPerDownEvent(ctx context.Context, pool *pgxpool.Pool, pendingGrace time.Duration) ([]alertViolation, error) {
	rows, err := pool.Query(ctx, `
		SELECT e.id, e.monitor_id, e.created_at,
		       count(a.id) FILTER (WHERE NOT a.is_reminder) AS non_reminders,
		       count(a.id) FILTER (WHERE NOT a.is_reminder AND a.status = 'pending') AS still_pending
		FROM events e
		LEFT JOIN alerts a ON a.event_id = e.id
		WHERE e.type = 'down'
		GROUP BY e.id, e.monitor_id, e.created_at
		HAVING count(a.id) FILTER (WHERE NOT a.is_reminder) != 1
		    OR (count(a.id) FILTER (WHERE NOT a.is_reminder AND a.status = 'pending') > 0
		        AND e.created_at < now() - $1::interval)
		ORDER BY e.created_at
	`, pendingGrace.String())
	if err != nil {
		return nil, fmt.Errorf("checkAlertPerDownEvent: query: %w", err)
	}
	defer rows.Close()

	var out []alertViolation
	for rows.Next() {
		var v alertViolation
		if err := rows.Scan(&v.EventID, &v.MonitorID, &v.EventCreatedAt, &v.NonReminders, &v.StillPending); err != nil {
			return nil, fmt.Errorf("checkAlertPerDownEvent: scan: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("checkAlertPerDownEvent: rows: %w", err)
	}
	return out, nil
}

// staleLate is a monitor stuck in "late" more than 2 minutes past its
// down-threshold deadline (next_deadline already holds occurrence+grace for a
// late monitor — see backend/store/scheduler.go's MarkMonitorLate).
type staleLate struct {
	MonitorID    string
	Slug         string
	NextDeadline time.Time
	OverdueBy    time.Duration
}

func checkNoStuckLate(ctx context.Context, pool *pgxpool.Pool) ([]staleLate, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, slug, next_deadline, now() - next_deadline AS overdue
		FROM monitors
		WHERE state = 'late' AND next_deadline < now() - interval '2 minutes'
		ORDER BY next_deadline
	`)
	if err != nil {
		return nil, fmt.Errorf("checkNoStuckLate: query: %w", err)
	}
	defer rows.Close()

	var out []staleLate
	for rows.Next() {
		var s staleLate
		if err := rows.Scan(&s.MonitorID, &s.Slug, &s.NextDeadline, &s.OverdueBy); err != nil {
			return nil, fmt.Errorf("checkNoStuckLate: scan: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("checkNoStuckLate: rows: %w", err)
	}
	return out, nil
}

// duplicateTransition is a monitor with two consecutive up/down events of the
// same type and no opposite-type event between them (e.g. down, down).
type duplicateTransition struct {
	MonitorID string
	EventID   int64
	Type      string
	CreatedAt time.Time
}

func checkNoDuplicateTransitions(ctx context.Context, pool *pgxpool.Pool) ([]duplicateTransition, error) {
	rows, err := pool.Query(ctx, `
		SELECT monitor_id, id, type, created_at
		FROM (
			SELECT id, monitor_id, type, created_at,
			       lag(type) OVER (PARTITION BY monitor_id ORDER BY created_at, id) AS prev_type
			FROM events
			WHERE type IN ('up', 'down')
		) t
		WHERE type = prev_type
		ORDER BY created_at
	`)
	if err != nil {
		return nil, fmt.Errorf("checkNoDuplicateTransitions: query: %w", err)
	}
	defer rows.Close()

	var out []duplicateTransition
	for rows.Next() {
		var d duplicateTransition
		if err := rows.Scan(&d.MonitorID, &d.EventID, &d.Type, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("checkNoDuplicateTransitions: scan: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("checkNoDuplicateTransitions: rows: %w", err)
	}
	return out, nil
}

func report(missingOrDup []alertViolation, stuckLate []staleLate, dupTransitions []duplicateTransition) {
	if len(missingOrDup) > 0 {
		fmt.Printf("\n[FAIL] %d down event(s) without exactly one resolved alert:\n", len(missingOrDup))
		for _, v := range missingOrDup {
			fmt.Printf("  event=%d monitor=%s created_at=%s non_reminder_alerts=%d still_pending=%d\n",
				v.EventID, v.MonitorID, v.EventCreatedAt.Format(time.RFC3339), v.NonReminders, v.StillPending)
		}
	}
	if len(stuckLate) > 0 {
		fmt.Printf("\n[FAIL] %d monitor(s) stuck late beyond grace+2min:\n", len(stuckLate))
		for _, s := range stuckLate {
			fmt.Printf("  monitor=%s slug=%s next_deadline=%s overdue_by=%s\n",
				s.MonitorID, s.Slug, s.NextDeadline.Format(time.RFC3339), s.OverdueBy)
		}
	}
	if len(dupTransitions) > 0 {
		fmt.Printf("\n[FAIL] %d duplicate consecutive transition(s):\n", len(dupTransitions))
		for _, d := range dupTransitions {
			fmt.Printf("  monitor=%s event=%d type=%s created_at=%s\n",
				d.MonitorID, d.EventID, d.Type, d.CreatedAt.Format(time.RFC3339))
		}
	}
}
