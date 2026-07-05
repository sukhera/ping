// Package prober runs the HTTP-monitor probe loop: every tick it claims due
// http monitors and probes each target through an SSRF-guarded client,
// applying the same fail-streak confirmation and event/alert-outbox pattern
// the scheduler and check-in path use for heartbeat monitors. Package
// direction: worker/prober → store (TECH-PLAN package-direction rule).
package prober

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

const (
	// maxBodyBytes bounds how much of a probe's response body is read for
	// keyword matching (TECH-PLAN §2.2). Bodies are never persisted, win or
	// lose — only the match result, status, and latency are recorded.
	maxBodyBytes = 64 << 10

	// maxRedirects caps redirect hops when http_config.follow_redirects is
	// set, so a redirect loop can't hang a probe past its own timeout anyway,
	// but this keeps the hop count itself bounded and sane.
	maxRedirects = 5
)

// httpConfig is the decoded shape of a monitor's http_config JSONB column
// (mirrored in frontend/types/monitor.ts's HTTPConfig). All fields are
// optional; the zero value means "use the default" for each.
type httpConfig struct {
	Headers         map[string]string `json:"headers"`
	Keyword         string            `json:"keyword"`
	KeywordNegate   bool              `json:"keyword_negate"`
	FollowRedirects *bool             `json:"follow_redirects"`
}

// followRedirects reports whether redirects should be followed, defaulting
// to true (PRD F2.1) when unset.
func (c httpConfig) followRedirects() bool {
	if c.FollowRedirects == nil {
		return true
	}
	return *c.FollowRedirects
}

// decodeHTTPConfig parses a monitor's http_config column, treating empty/nil
// input as the zero value (all defaults) rather than an error — a monitor
// created before any advanced fields were set stores '{}'.
func decodeHTTPConfig(raw []byte) (httpConfig, error) {
	var c httpConfig
	if len(raw) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return httpConfig{}, fmt.Errorf("prober: decode http_config: %w", err)
	}
	return c, nil
}

// resolver is the DNS lookup the dial guard uses, injected so tests can
// simulate a hostname that resolves to a different (private) address than a
// test's own expectations — the "public hostname re-resolves privately" AC.
type resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// newHTTPClient builds an SSRF-guarded *http.Client for one probe attempt.
// timeout bounds the whole request (connect + TLS + headers + body read);
// allowlist is the operator's SSRF_ALLOWLIST override (empty = no override,
// guard fully enforced); res is the DNS resolver (net.DefaultResolver in
// production, faked in tests).
func newHTTPClient(timeout time.Duration, allowlist []netip.Prefix, res resolver, followRedirects bool) *http.Client {
	dialer := &net.Dialer{Timeout: timeout}

	guardedDial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("prober: split host:port: %w", err)
		}

		ip, err := resolveAndGuard(ctx, res, network, host, allowlist)
		if err != nil {
			return nil, err
		}

		// Dial the validated IP directly (not the original host) so a second
		// DNS lookup inside net.Dialer can't re-resolve to a different
		// (unguarded) address — this is what closes the TOCTOU gap the AC
		// calls out ("public hostname re-resolves privately").
		return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}

	transport := &http.Transport{
		DialContext: guardedDial,
		// TLSClientConfig deliberately left at Go's secure default
		// (certificate verification on) — probes must not silently accept
		// invalid certs, and a TLS failure is one of the distinct recorded
		// error strings the AC requires.
		TLSHandshakeTimeout: timeout,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	if !followRedirects {
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	} else {
		client.CheckRedirect = func(_ *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("prober: stopped after %d redirects", maxRedirects)
			}
			return nil
		}
	}

	return client
}

// resolveAndGuard resolves host and returns the first resolved address that
// passes the SSRF guard, or an error if host is a raw IP that fails the
// guard, or every resolved address is disallowed. allowlist entries bypass
// the guard entirely (self-hosted internal targets, operator opt-in).
func resolveAndGuard(ctx context.Context, res resolver, network, host string, allowlist []netip.Prefix) (netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return guardAddr(addr, allowlist)
	}

	addrs, err := res.LookupNetIP(ctx, netipNetwork(network), host)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("prober: dns lookup failed: %w", err)
	}
	if len(addrs) == 0 {
		return netip.Addr{}, errors.New("prober: dns lookup returned no addresses")
	}
	// Only the first resolved address is dialed (matches net.Dialer's default
	// single-attempt behavior for a guarded custom DialContext); guard it
	// before returning so the caller dials only a validated address.
	return guardAddr(addrs[0], allowlist)
}

// netipNetwork maps an http.Transport dial network ("tcp", "tcp4", "tcp6")
// to the network argument LookupNetIP expects ("ip", "ip4", "ip6").
func netipNetwork(network string) string {
	switch {
	case strings.HasSuffix(network, "4"):
		return "ip4"
	case strings.HasSuffix(network, "6"):
		return "ip6"
	default:
		return "ip"
	}
}

// guardAddr rejects addr if it is private/loopback/link-local/multicast/
// unspecified (TECH-PLAN §2.2's SSRF guard) unless it falls within an
// operator-configured allowlist prefix.
func guardAddr(addr netip.Addr, allowlist []netip.Prefix) (netip.Addr, error) {
	addr = addr.Unmap()
	for _, prefix := range allowlist {
		if prefix.Contains(addr) {
			return addr, nil
		}
	}
	if isBlockedAddr(addr) {
		return netip.Addr{}, fmt.Errorf("prober: target resolves to a disallowed address (%s)", addr)
	}
	return addr, nil
}

// isBlockedAddr reports whether addr falls in a private/loopback/link-local/
// multicast/unspecified range. 169.254.169.254 (the common cloud metadata
// address) is covered by IsLinkLocalUnicast, but is named in the doc comment
// since it's the specific target the AC calls out.
func isBlockedAddr(addr netip.Addr) bool {
	return addr.IsPrivate() ||
		addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() || // covers 169.254.0.0/16, incl. 169.254.169.254
		addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified() ||
		!addr.IsGlobalUnicast()
}

// probeError classifies a probe failure into one of the AC's distinct,
// user-safe error strings — never the raw Go error, which can leak internal
// detail (local paths, resolver internals).
func probeError(err error) string {
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return "dns lookup failed"
	}

	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return "tls certificate error"
	}
	if strings.Contains(err.Error(), "tls:") || strings.Contains(err.Error(), "x509:") {
		return "tls handshake failed"
	}

	if strings.Contains(err.Error(), "disallowed address") {
		return "target address not allowed"
	}

	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return "request timed out"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "request timed out"
	}

	return "request failed"
}

// probeResult is one probe attempt's outcome, before it is translated into a
// store.ProbeOutcome by the caller.
type probeResult struct {
	OK         bool
	HTTPStatus *int32
	LatencyMS  int32
	Error      string
}

// runProbe executes one HTTP probe against a monitor's configured target,
// evaluating the status/keyword assertions and returning a classified
// outcome. ctx should already carry the monitor's timeout_s deadline.
func runProbe(ctx context.Context, client *http.Client, method, url string, cfg httpConfig) probeResult {
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return probeResult{Error: "invalid request"}
	}
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	latency := latencyMS(time.Since(start))
	if err != nil {
		return probeResult{LatencyMS: latency, Error: probeError(err)}
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			slog.WarnContext(ctx, "prober: close response body failed", "error", cerr)
		}
	}()

	// resp.StatusCode is always a 3-digit HTTP status, well within int32 range.
	status := int32(resp.StatusCode) //nolint:gosec // bounded by net/http

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return probeResult{HTTPStatus: &status, LatencyMS: latency, Error: "failed reading response body"}
	}

	if !isSuccessStatus(resp.StatusCode) {
		return probeResult{HTTPStatus: &status, LatencyMS: latency, Error: fmt.Sprintf("unexpected status %d", resp.StatusCode)}
	}

	if cfg.Keyword != "" {
		found := strings.Contains(string(body), cfg.Keyword)
		if found == cfg.KeywordNegate {
			return probeResult{HTTPStatus: &status, LatencyMS: latency, Error: "keyword assertion failed"}
		}
	}

	return probeResult{OK: true, HTTPStatus: &status, LatencyMS: latency}
}

// isSuccessStatus applies the PRD F2.1 default expected-status policy
// (2xx/3xx) — there is no per-monitor override in this ticket.
func isSuccessStatus(code int) bool {
	return code >= 200 && code < 400
}

// latencyMS converts a probe duration to milliseconds, clamping to
// math.MaxInt32 instead of overflowing — a probe is bounded by its own
// timeout_s (max 30s per PRD F2.1) so this never triggers in practice, but a
// clamp is cheap defense against a future caller passing an unbounded duration.
func latencyMS(d time.Duration) int32 {
	ms := d.Milliseconds()
	if ms > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(ms) //nolint:gosec // bounds-checked above
}
