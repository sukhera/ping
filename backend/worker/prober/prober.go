package prober

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/sukhera/ping/internal/testclock"
	"github.com/sukhera/ping/store"
	"github.com/sukhera/ping/worker"
)

const (
	// DefaultInterval is the production tick cadence. Unlike the scheduler's
	// next_deadline (a single global cadence), each http monitor has its own
	// interval_s enforced via next_probe_at; the tick only needs to be
	// frequent enough to notice newly-due monitors promptly.
	DefaultInterval = 10 * time.Second

	// claimLimit caps monitors claimed per tick, matching the scheduler's
	// bound so one tick's claim transaction stays small; the next tick picks
	// up any remainder.
	claimLimit = 500

	// poolLimit bounds concurrent in-flight probes per tick (TECH-PLAN §2.2).
	poolLimit = 16

	// defaultFailThreshold is PRD F2.2's default confirmation threshold, used
	// when a monitor's fail_threshold column is unset.
	defaultFailThreshold = 2
)

// Store is the slice of *store.Store the prober needs, as an interface so the
// loop can be tested without Postgres.
type Store interface {
	ClaimDueProbes(ctx context.Context, now time.Time, limit int32) ([]store.Monitor, error)
	RecordProbeResult(ctx context.Context, o store.ProbeOutcome) (store.RecordProbeResultResult, error)
}

// Prober probes claimed http monitors through an SSRF-guarded client and
// records the outcome. allowlist lets a self-hoster opt specific internal
// ranges back into probing (SSRF_ALLOWLIST); empty means the guard is fully
// enforced.
type Prober struct {
	st        Store
	allowlist []netip.Prefix
	res       resolver
}

// New builds a Prober. res is the DNS resolver used by the SSRF guard;
// production passes net.DefaultResolver.
func New(st Store, allowlist []netip.Prefix) *Prober {
	return &Prober{st: st, allowlist: allowlist, res: net.DefaultResolver}
}

// Run starts the prober loop and blocks until ctx is cancelled (graceful
// stop). interval is a parameter so integration tests can drive fast ticks;
// production passes DefaultInterval.
func Run(ctx context.Context, st Store, hb *worker.Heartbeat, interval time.Duration, allowlist []netip.Prefix) error {
	return New(st, allowlist).run(ctx, hb, interval)
}

// RunOnce performs a single claim-and-probe pass outside the loop's own
// ticking — used by the e2e-only /test/advance-clock endpoint
// (backend/server/testclock.go) so a time-warp takes effect immediately
// without waiting for the next interval, and works identically whether or not
// the worker role is running.
func RunOnce(ctx context.Context, st Store, allowlist []netip.Prefix) error {
	return New(st, allowlist).tick(ctx)
}

func (p *Prober) run(ctx context.Context, hb *worker.Heartbeat, interval time.Duration) error {
	return worker.Run(ctx, worker.Config{
		Name:     "prober",
		Interval: interval,
		Jitter:   interval / 8,
		HB:       hb,
		Tick:     p.tick,
	})
}

// tick claims due http monitors and probes each through a pool bounded at
// poolLimit concurrent probes, so a burst of due monitors can't spawn
// unbounded goroutines or overwhelm the outbound network. A single monitor's
// probe/record failure is logged and does not fail the tick — matching the
// scheduler and alerter's per-item isolation.
func (p *Prober) tick(ctx context.Context) error {
	monitors, err := p.st.ClaimDueProbes(ctx, testclock.Now(), claimLimit)
	if err != nil {
		return err
	}
	if len(monitors) == 0 {
		return nil
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(poolLimit)

	for _, m := range monitors {
		g.Go(func() error {
			p.probeOne(gctx, m)
			return nil
		})
	}
	// probeOne never returns an error (failures are recorded, not
	// propagated), so Wait only reports gctx cancellation.
	return g.Wait()
}

// probeOne runs and records a single monitor's probe. It never returns an
// error: a probe or store failure is logged and skipped so one bad target
// can't stall the tick's errgroup.
func (p *Prober) probeOne(ctx context.Context, m store.Monitor) {
	log := slog.With("monitor", m.Slug)

	timeout := time.Duration(int32Or(m.TimeoutS, 10)) * time.Second
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cfg, err := decodeHTTPConfig(m.HTTPConfig)
	if err != nil {
		log.WarnContext(ctx, "prober: bad http_config, skipping", "error", err)
		return
	}

	client := newHTTPClient(timeout, p.allowlist, p.res, cfg.followRedirects())
	method := m.Method
	if method == "" {
		method = "GET"
	}

	result := runProbe(probeCtx, client, method, m.URL, cfg)

	intervalS := int32Or(m.IntervalS, 60)
	now := testclock.Now()
	outcome := store.ProbeOutcome{
		MonitorID:               m.ID,
		OK:                      result.OK,
		HTTPStatus:              result.HTTPStatus,
		LatencyMS:               &result.LatencyMS,
		Error:                   result.Error,
		TLSExpiresAt:            result.TLSExpiresAt,
		FailThreshold:           int32Or(m.FailThreshold, defaultFailThreshold),
		PriorState:              m.State,
		PriorFailStreak:         m.FailStreak,
		PriorTLSWarnedExpiresAt: m.TLSWarnedExpiresAt,
		NextProbeAt:             now.Add(time.Duration(intervalS) * time.Second),
		Now:                     now,
	}

	res, err := p.st.RecordProbeResult(ctx, outcome)
	if err != nil {
		log.WarnContext(ctx, "prober: record result failed", "error", err)
		return
	}
	if res.Transition != "" {
		slog.InfoContext(ctx, "prober transition", "monitor", m.Slug, "transition", res.Transition)
	}
}

func int32Or(v *int32, def int32) int32 {
	if v == nil {
		return def
	}
	return *v
}
