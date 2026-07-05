package prober

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

// loopbackClient builds a client via newHTTPClient with loopback allowed,
// exercising the real dialer/guard/transport (including real DNS resolution
// via net.DefaultResolver) rather than a stubbed client — httptest servers
// only bind to 127.0.0.1/::1.
func loopbackClient(timeout time.Duration, followRedirects bool) *http.Client {
	allow := []netip.Prefix{
		netip.MustParsePrefix("127.0.0.1/32"),
		netip.MustParsePrefix("::1/128"),
	}
	return newHTTPClient(timeout, allow, net.DefaultResolver, followRedirects)
}

func TestRunProbe_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := loopbackClient(2*time.Second, true)
	res := runProbe(context.Background(), client, "GET", srv.URL, httpConfig{})
	if !res.OK {
		t.Fatalf("runProbe = %+v, want OK", res)
	}
}

func TestRunProbe_UnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := loopbackClient(2*time.Second, true)
	res := runProbe(context.Background(), client, "GET", srv.URL, httpConfig{})
	if res.OK {
		t.Fatal("runProbe = OK, want failure for 500 status")
	}
	if res.Error == "" {
		t.Error("runProbe error string is empty, want a distinct message")
	}
}

func TestRunProbe_KeywordMiss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("all good here"))
	}))
	defer srv.Close()

	client := loopbackClient(2*time.Second, true)
	res := runProbe(context.Background(), client, "GET", srv.URL, httpConfig{Keyword: "expected-but-absent"})
	if res.OK {
		t.Fatal("runProbe = OK, want keyword-miss failure")
	}
	if res.Error != "keyword assertion failed" {
		t.Errorf("runProbe.Error = %q, want %q", res.Error, "keyword assertion failed")
	}
}

func TestRunProbe_KeywordNegateMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("error: something broke"))
	}))
	defer srv.Close()

	client := loopbackClient(2*time.Second, true)
	res := runProbe(context.Background(), client, "GET", srv.URL, httpConfig{Keyword: "error", KeywordNegate: true})
	if res.OK {
		t.Fatal("runProbe = OK, want negated-keyword failure (keyword present but negated)")
	}
}

func TestRunProbe_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := loopbackClient(20*time.Millisecond, true)
	res := runProbe(context.Background(), client, "GET", srv.URL, httpConfig{})
	if res.OK {
		t.Fatal("runProbe = OK, want timeout failure")
	}
	if res.Error != "request timed out" {
		t.Errorf("runProbe.Error = %q, want %q", res.Error, "request timed out")
	}
}

func TestRunProbe_DNSFailure(t *testing.T) {
	client := loopbackClient(2*time.Second, true)
	res := runProbe(context.Background(), client, "GET", "http://this-host-does-not-resolve.invalid", httpConfig{})
	if res.OK {
		t.Fatal("runProbe = OK, want DNS failure")
	}
	if res.Error != "dns lookup failed" {
		t.Errorf("runProbe.Error = %q, want %q", res.Error, "dns lookup failed")
	}
}

func TestRunProbe_SSRFRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// No allowlist: loopback is rejected by the guard even though the server
	// is real and reachable — proves the guard is wired into the real client,
	// not just unit-tested in isolation.
	client := newHTTPClient(2*time.Second, nil, net.DefaultResolver, true)
	res := runProbe(context.Background(), client, "GET", srv.URL, httpConfig{})
	if res.OK {
		t.Fatal("runProbe = OK, want SSRF rejection for loopback target")
	}
	if res.Error != "target address not allowed" {
		t.Errorf("runProbe.Error = %q, want %q", res.Error, "target address not allowed")
	}
}
