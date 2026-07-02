package server

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// TestShutdown_WaitsForInFlightRequest proves graceful shutdown drains an
// active request rather than cutting it off, using a slow handler and a real
// listening server (http.Server.Shutdown only blocks for connections it's
// actually tracking, so httptest.NewRecorder can't exercise this path).
func TestShutdown_WaitsForInFlightRequest(t *testing.T) {
	var handlerFinished atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		handlerFinished.Store(true)
		w.WriteHeader(http.StatusOK)
	})

	ctx := context.Background()
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	srv := &http.Server{Handler: mux}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	client := &http.Client{Timeout: 2 * time.Second}
	reqDone := make(chan struct{})
	go func() {
		defer close(reqDone)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+ln.Addr().String()+"/slow", nil)
		if err != nil {
			t.Errorf("NewRequestWithContext: %v", err)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Errorf("client.Do: %v", err)
			return
		}
		defer resp.Body.Close() //nolint:errcheck
	}()

	// Give the request time to reach the handler before shutting down.
	time.Sleep(50 * time.Millisecond)

	if err := Shutdown(context.Background(), srv); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	<-reqDone

	if !handlerFinished.Load() {
		t.Fatal("expected in-flight handler to complete before Shutdown returned")
	}
	if err := <-serveErr; err != nil && err != http.ErrServerClosed {
		t.Errorf("srv.Serve: %v", err)
	}
}
