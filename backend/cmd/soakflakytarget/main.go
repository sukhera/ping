// Command soakflakytarget is a standalone HTTP target for the PING-023 soak
// harness (hack/soak/). It serves one /health endpoint per configured port,
// flipping between 200 and 503 on an independent random timer per port, so
// the soak run's HTTP monitors see real, continuous up/down flapping — the
// same toggling shape as frontend/e2e/http-monitor-lifecycle.spec.ts's
// MockTarget, ported to Go so it runs standalone for the full 48h rather than
// living inside a single Playwright test.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {
	ports := flag.String("ports", "9101", "comma-separated list of ports to serve, one flaky target per port")
	minFlipS := flag.Int("min-flip-s", 20, "minimum seconds between health flips")
	maxFlipS := flag.Int("max-flip-s", 180, "maximum seconds between health flips")
	flag.Parse()

	handler := slog.NewTextHandler(os.Stdout, nil)
	slog.SetDefault(slog.New(handler))

	if err := run(*ports, *minFlipS, *maxFlipS); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(portsCSV string, minFlipS, maxFlipS int) error {
	if minFlipS <= 0 || maxFlipS <= minFlipS {
		return fmt.Errorf("run: invalid flip window: min-flip-s=%d max-flip-s=%d", minFlipS, maxFlipS)
	}

	portStrs := strings.Split(portsCSV, ",")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	var wg sync.WaitGroup
	errCh := make(chan error, len(portStrs))

	for _, ps := range portStrs {
		ps = strings.TrimSpace(ps)
		if ps == "" {
			continue
		}
		port, err := strconv.Atoi(ps)
		if err != nil {
			return fmt.Errorf("run: invalid port %q: %w", ps, err)
		}

		t := newTarget(port, time.Duration(minFlipS)*time.Second, time.Duration(maxFlipS)*time.Second)
		wg.Go(func() {
			t.flipLoop(ctx)
		})
		wg.Go(func() {
			if err := t.serve(ctx); err != nil {
				errCh <- err
			}
		})
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

// target is one flaky /health endpoint. healthy is an atomic flag flipped
// independently of request handling so flips happen on their own timer
// regardless of probe traffic.
type target struct {
	port    int
	minFlip time.Duration
	maxFlip time.Duration
	healthy atomic.Bool
}

func newTarget(port int, minFlip, maxFlip time.Duration) *target {
	t := &target{port: port, minFlip: minFlip, maxFlip: maxFlip}
	t.healthy.Store(true)
	return t
}

// flipLoop toggles health on a random interval in [minFlip, maxFlip) until ctx
// is done — never synchronized with any other target or the harness's chaos
// loop, so transitions land at genuinely unpredictable points.
func (t *target) flipLoop(ctx context.Context) {
	for {
		wait := t.minFlip + time.Duration(rand.Int64N(int64(t.maxFlip-t.minFlip))) //nolint:gosec // non-crypto jitter
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
			next := !t.healthy.Load()
			t.healthy.Store(next)
			slog.Info("flip", "port", t.port, "healthy", next)
		}
	}
}

func (t *target) serve(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		if t.healthy.Load() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("unhealthy"))
	})

	srv := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", t.port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("target listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("serve port %d: %w", t.port, err)
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown port %d: %w", t.port, err)
		}
		return <-errCh
	case err := <-errCh:
		return err
	}
}
