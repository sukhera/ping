package prober

import (
	"context"
	"net/netip"
	"testing"
)

func TestIsBlockedAddr(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want bool
	}{
		{"metadata service", "169.254.169.254", true},
		{"loopback v4", "127.0.0.1", true},
		{"loopback v6", "::1", true},
		{"private class A", "10.0.0.1", true},
		{"private class B", "172.16.0.1", true},
		{"private class C", "192.168.1.1", true},
		{"link-local", "169.254.1.1", true},
		{"unspecified v4", "0.0.0.0", true},
		{"multicast", "224.0.0.1", true},
		{"public v4", "93.184.216.34", false},
		{"public v6", "2606:2800:220:1:248:1893:25c8:1946", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := netip.MustParseAddr(tt.addr)
			if got := isBlockedAddr(addr); got != tt.want {
				t.Errorf("isBlockedAddr(%s) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestGuardAddr_AllowlistOverride(t *testing.T) {
	private := netip.MustParseAddr("10.1.2.3")
	allowlist := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}

	if _, err := guardAddr(private, allowlist); err != nil {
		t.Errorf("guardAddr with matching allowlist entry returned %v, want nil", err)
	}

	if _, err := guardAddr(private, nil); err == nil {
		t.Error("guardAddr with no allowlist returned nil error, want rejection")
	}
}

// fakeResolver simulates DNS returning a specific set of addresses for any
// host, independent of real DNS — used to test the guard against addresses a
// test controls precisely, including the "public hostname re-resolves
// privately" TOCTOU case.
type fakeResolver struct {
	addrs []netip.Addr
	err   error
}

func (f fakeResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.addrs, nil
}

// TestResolveAndGuard_RejectsPrivateResolution covers the AC: "Target
// resolving to 169.254.169.254 / 127.0.0.1 / 10.x rejected at dial time, even
// when a public hostname re-resolves privately." The guard must validate the
// address the resolver actually returned — not any earlier assumption about
// the hostname — so a resolver that (this tick) returns a private address for
// what looks like a public hostname must still be rejected.
func TestResolveAndGuard_RejectsPrivateResolution(t *testing.T) {
	res := fakeResolver{addrs: []netip.Addr{netip.MustParseAddr("169.254.169.254")}}

	_, err := resolveAndGuard(context.Background(), res, "tcp4", "looks-public.example.com", nil)
	if err == nil {
		t.Fatal("resolveAndGuard returned nil error for a private resolution, want rejection")
	}
}

func TestResolveAndGuard_AllowsPublicResolution(t *testing.T) {
	res := fakeResolver{addrs: []netip.Addr{netip.MustParseAddr("93.184.216.34")}}

	addr, err := resolveAndGuard(context.Background(), res, "tcp4", "example.com", nil)
	if err != nil {
		t.Fatalf("resolveAndGuard returned %v, want nil", err)
	}
	if addr.String() != "93.184.216.34" {
		t.Errorf("resolveAndGuard returned %s, want 93.184.216.34", addr)
	}
}

func TestResolveAndGuard_RawIPBypassesDNS(t *testing.T) {
	res := fakeResolver{err: nil} // never consulted for a raw IP host

	if _, err := resolveAndGuard(context.Background(), res, "tcp4", "127.0.0.1", nil); err == nil {
		t.Fatal("resolveAndGuard(127.0.0.1) returned nil error, want rejection")
	}
}
