// Command soakloadgen drives synthetic traffic for the PING-023 soak harness
// (hack/soak/): it registers one test user, creates a mix of heartbeat and
// HTTP monitors against the running ping API, then continuously pings the
// heartbeat monitors, occasionally skipping a ping or sending an explicit
// failure, for the full soak run duration. HTTP monitors point at a
// soakflakytarget instance (started separately by run.sh), which flips its
// own health on an independent timer — loadgen only creates the monitor and
// leaves probing to the prober worker and health-flipping to the target.
//
// It talks to the app exclusively over its public HTTP API (never touches the
// database directly) so it exercises the exact same code paths real traffic
// would, including surviving the chaos loop's restarts: a connection refused
// or timeout while the app is mid-restart is retried, not treated as a
// monitor failure to report.
package main

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func main() {
	baseURL := flag.String("base-url", "http://localhost:8080", "ping API base URL")
	monitorCount := flag.Int("monitor-count", 10, "total synthetic monitors to create (split evenly heartbeat/http)")
	flakyTargetBaseURL := flag.String("flaky-target-base-url", "http://127.0.0.1", "base URL (scheme+host, no port) of the soakflakytarget instances")
	flakyTargetBasePort := flag.Int("flaky-target-base-port", 9101, "first port used by soakflakytarget; HTTP monitor i probes base-port+i")
	skipProbability := flag.Float64("skip-probability", 0.15, "probability a heartbeat ping is simply skipped (simulates a dropped cron run)")
	failProbability := flag.Float64("fail-probability", 0.05, "probability a heartbeat ping reports explicit failure instead of success")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cfg := config{
		baseURL:             *baseURL,
		monitorCount:        *monitorCount,
		flakyTargetBaseURL:  *flakyTargetBaseURL,
		flakyTargetBasePort: *flakyTargetBasePort,
		skipProbability:     *skipProbability,
		failProbability:     *failProbability,
	}

	if err := run(ctx, cfg); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

type config struct {
	baseURL             string
	monitorCount        int
	flakyTargetBaseURL  string
	flakyTargetBasePort int
	skipProbability     float64
	failProbability     float64
}

func run(ctx context.Context, cfg config) error {
	client := &http.Client{Timeout: 10 * time.Second}
	api := &apiClient{baseURL: cfg.baseURL, http: client}

	token, err := bootstrapAccount(ctx, api)
	if err != nil {
		return fmt.Errorf("run: bootstrap account: %w", err)
	}
	api.bearer = token

	heartbeatCount := cfg.monitorCount / 2
	httpCount := cfg.monitorCount - heartbeatCount

	heartbeats, err := createHeartbeatMonitors(ctx, api, heartbeatCount)
	if err != nil {
		return fmt.Errorf("run: create heartbeat monitors: %w", err)
	}
	slog.Info("created heartbeat monitors", "count", len(heartbeats))

	if err := createHTTPMonitors(ctx, api, httpCount, cfg.flakyTargetBaseURL, cfg.flakyTargetBasePort); err != nil {
		return fmt.Errorf("run: create http monitors: %w", err)
	}
	slog.Info("created http monitors; probing and health-flipping are handled by the prober worker and soakflakytarget respectively", "count", httpCount)

	var wg sync.WaitGroup
	for _, m := range heartbeats {
		wg.Go(func() {
			pingLoop(ctx, client, cfg.baseURL, m, cfg.skipProbability, cfg.failProbability)
		})
	}
	wg.Wait()
	return nil
}

// heartbeatMonitor is one created heartbeat monitor's ping endpoint and its
// configured period, so pingLoop can pace itself to the monitor's own cadence.
type heartbeatMonitor struct {
	slug    string
	pingURL string
	periodS int32
}

// bootstrapAccount registers a fresh soak-test user and returns a bearer
// access token. PING_ENV=test (set by hack/soak/run.sh) disables the auth
// rate limiter for this one-time setup burst, same escape hatch the E2E suite
// relies on (see docs/DEVELOPMENT.md's "E2E auth rate limit" section).
func bootstrapAccount(ctx context.Context, api *apiClient) (string, error) {
	email := fmt.Sprintf("soak-%s@example.com", randomHex(8))
	const password = "soak-test-password-1"

	var authResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := api.post(ctx, "/api/v1/auth/register", map[string]string{
		"email":    email,
		"password": password,
	}, &authResp); err != nil {
		return "", fmt.Errorf("bootstrapAccount: register: %w", err)
	}
	return authResp.AccessToken, nil
}

func createHeartbeatMonitors(ctx context.Context, api *apiClient, count int) ([]heartbeatMonitor, error) {
	const periodS = 60 // schedule.MinPeriod floor: packs maximum cycles into the soak window
	const graceS = 60  // schedule.MinGrace floor

	monitors := make([]heartbeatMonitor, 0, count)
	for i := range count {
		var resp struct {
			Slug    string `json:"slug"`
			PingURL string `json:"ping_url"`
		}
		err := api.post(ctx, "/api/v1/monitors", map[string]any{
			"kind":          "heartbeat",
			"name":          fmt.Sprintf("soak-heartbeat-%d", i),
			"schedule_kind": "period",
			"period_s":      periodS,
			"grace_s":       graceS,
			"tz":            "UTC",
		}, &resp)
		if err != nil {
			return nil, fmt.Errorf("createHeartbeatMonitors: %w", err)
		}
		monitors = append(monitors, heartbeatMonitor{slug: resp.Slug, pingURL: resp.PingURL, periodS: periodS})
	}
	return monitors, nil
}

func createHTTPMonitors(ctx context.Context, api *apiClient, count int, flakyTargetBaseURL string, basePort int) error {
	const intervalS = 30 // minIntervalS floor (backend/server/monitor.go)
	const timeoutS = 5
	const failThreshold = 2

	for i := range count {
		targetURL := fmt.Sprintf("%s:%d/health", flakyTargetBaseURL, basePort+i)

		err := api.post(ctx, "/api/v1/monitors", map[string]any{
			"kind":           "http",
			"name":           fmt.Sprintf("soak-http-%d", i),
			"url":            targetURL,
			"method":         "GET",
			"interval_s":     intervalS,
			"timeout_s":      timeoutS,
			"fail_threshold": failThreshold,
		}, nil)
		if err != nil {
			return fmt.Errorf("createHTTPMonitors: %w", err)
		}
	}
	return nil
}

// pingLoop sends heartbeat pings on the monitor's own period, skipping a ping
// or sending an explicit failure with the configured probabilities so the
// monitor genuinely cycles through up/late/down over the soak run rather than
// staying up forever. Connection errors (app mid-restart from the chaos loop)
// are logged and retried on the next tick, never recorded as a monitor
// failure — that would conflate harness chaos with a real check-in.
func pingLoop(ctx context.Context, client *http.Client, baseURL string, m heartbeatMonitor, skipProbability, failProbability float64) {
	ticker := time.NewTicker(time.Duration(m.periodS) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sendOnePing(ctx, client, baseURL, m, skipProbability, failProbability)
		}
	}
}

func sendOnePing(ctx context.Context, client *http.Client, baseURL string, m heartbeatMonitor, skipProbability, failProbability float64) {
	roll := rand.Float64() //nolint:gosec // non-crypto traffic-shaping roll
	switch {
	case roll < skipProbability:
		return // dropped cron run: no request at all
	case roll < skipProbability+failProbability:
		pingURL(ctx, client, baseURL+"/p/"+m.slug+"/fail")
	default:
		pingURL(ctx, client, m.pingURL)
	}
}

func pingURL(ctx context.Context, client *http.Client, url string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		slog.Error("build ping request", "url", url, "error", err)
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("ping request failed, will retry next tick", "url", url, "error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = cryptorand.Read(b)
	return hex.EncodeToString(b)
}

// apiClient is a tiny JSON-over-HTTP client for the ping management API.
type apiClient struct {
	baseURL string
	bearer  string
	http    *http.Client
}

func (c *apiClient) post(ctx context.Context, path string, body any, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("apiClient.post: marshal body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("apiClient.post: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("apiClient.post %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("apiClient.post %s: unexpected status %d", path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("apiClient.post %s: decode response: %w", path, err)
	}
	return nil
}
