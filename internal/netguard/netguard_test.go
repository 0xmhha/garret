package netguard

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

func pfx(s string) netip.Prefix { return netip.MustParsePrefix(s) }

func pfxs(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(ss))
	for _, s := range ss {
		out = append(out, pfx(s))
	}
	return out
}

func TestIsPrivate(t *testing.T) {
	tests := []struct {
		prefix netip.Prefix
		want   bool
	}{
		{pfx("192.168.1.0/24"), true},
		{pfx("10.0.0.0/8"), true},
		{pfx("172.16.0.0/12"), true},
		{pfx("172.31.255.0/24"), true},
		{pfx("fd12::/64"), true},
		{pfx("fe80::/64"), true},
		{pfx("::ffff:192.168.1.0/120"), true},
		{pfx("172.32.0.0/16"), false},
		{pfx("8.8.8.0/24"), false},
		{pfx("100.64.0.0/10"), false},
		{pfx("2001:db8::/32"), false},
		{pfx("192.168.0.0/15"), false},
		{pfx("fc00::/6"), false},
		{pfx("127.0.0.0/8"), false},
		{pfx("0.0.0.0/0"), false},
		{netip.Prefix{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.prefix.String(), func(t *testing.T) {
			if got := IsPrivate(tt.prefix); got != tt.want {
				t.Fatalf("IsPrivate(%v) = %v, want %v", tt.prefix, got, tt.want)
			}
		})
	}
}

func TestResolveAllowed(t *testing.T) {
	en0 := Interface{Name: "en0", Up: true, Prefixes: pfxs("192.168.1.23/24", "fe80::1/64")}
	en1 := Interface{Name: "en1", Up: true, Prefixes: pfxs("10.1.2.3/16")}
	utun := Interface{Name: "utun3", Up: true, Prefixes: pfxs("203.0.113.9/32")}
	lo := Interface{Name: "lo0", Up: true, Loopback: true, Prefixes: pfxs("127.0.0.1/8")}
	down := Interface{Name: "en2", Prefixes: pfxs("172.16.5.5/24")}
	v6only := Interface{Name: "en3", Up: true, Prefixes: pfxs("fd00::5/64")}

	tests := []struct {
		name     string
		explicit []netip.Prefix
		ifaces   []Interface
		want     []netip.Prefix
		wantErr  error
	}{
		{name: "one private interface", ifaces: []Interface{en0}, want: pfxs("192.168.1.0/24")},
		{name: "public interface ignored", ifaces: []Interface{utun, en0, lo}, want: pfxs("192.168.1.0/24")},
		{name: "two private interfaces", ifaces: []Interface{en0, en1}, wantErr: ErrAmbiguousNetwork},
		{name: "only loopback", ifaces: []Interface{lo}, wantErr: ErrNoPrivateNetwork},
		{name: "only down", ifaces: []Interface{down}, wantErr: ErrNoPrivateNetwork},
		{name: "no interfaces", wantErr: ErrNoPrivateNetwork},
		{name: "IPv6 only needs explicit", ifaces: []Interface{v6only}, wantErr: ErrNoPrivateNetwork},
		{name: "explicit is masked", explicit: pfxs("192.168.1.7/24"), ifaces: []Interface{en1}, want: pfxs("192.168.1.0/24")},
		{name: "explicit duplicates removed", explicit: pfxs("10.0.0.0/8", "10.9.9.9/8"), want: pfxs("10.0.0.0/8")},
		{name: "explicit sorted", explicit: pfxs("192.168.0.0/16", "10.0.0.0/8"), want: pfxs("10.0.0.0/8", "192.168.0.0/16")},
		{name: "explicit IPv6", explicit: pfxs("fd00::/64"), want: pfxs("fd00::/64")},
		{name: "explicit everything", explicit: pfxs("0.0.0.0/0"), wantErr: ErrPublicNetwork},
		{name: "explicit public", explicit: pfxs("10.0.0.0/8", "8.8.8.0/24"), wantErr: ErrPublicNetwork},
		{name: "explicit invalid", explicit: []netip.Prefix{{}}, wantErr: ErrPublicNetwork},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveAllowed(tt.explicit, tt.ifaces)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveAllowedAmbiguousListsCandidatesInOrder(t *testing.T) {
	a := Interface{Name: "en1", Up: true, Prefixes: pfxs("10.1.2.3/16")}
	b := Interface{Name: "en0", Up: true, Prefixes: pfxs("192.168.1.23/24")}
	for _, order := range [][]Interface{{a, b}, {b, a}} {
		_, err := ResolveAllowed(nil, order)
		want := "en0 192.168.1.0/24, en1 10.1.0.0/16"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to contain %q", err, want)
		}
	}
}

func TestResolveAllowedDoesNotModifyInput(t *testing.T) {
	explicit := pfxs("192.168.1.7/24", "10.0.0.0/8")
	orig := slices.Clone(explicit)
	if _, err := ResolveAllowed(explicit, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(explicit, orig) {
		t.Fatalf("input changed: %v, was %v", explicit, orig)
	}
}

func TestAllowed(t *testing.T) {
	set := pfxs("192.168.1.0/24")
	tests := []struct {
		name string
		set  []netip.Prefix
		peer netip.Addr
		want bool
	}{
		{"inside", set, netip.MustParseAddr("192.168.1.5"), true},
		{"outside", set, netip.MustParseAddr("192.168.2.5"), false},
		{"network address", set, netip.MustParseAddr("192.168.1.0"), true},
		{"broadcast address", set, netip.MustParseAddr("192.168.1.255"), true},
		{"IPv4 loopback, empty set", nil, netip.MustParseAddr("127.0.0.1"), true},
		{"IPv6 loopback, empty set", nil, netip.MustParseAddr("::1"), true},
		{"mapped IPv4", set, netip.MustParseAddr("::ffff:192.168.1.5"), true},
		{"mapped loopback", nil, netip.MustParseAddr("::ffff:127.0.0.1"), true},
		{"zone kept out of match", pfxs("fe80::/64"), netip.MustParseAddr("fe80::1%en0"), true},
		{"public", set, netip.MustParseAddr("8.8.8.8"), false},
		{"zero address", set, netip.Addr{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Allowed(tt.set, tt.peer); got != tt.want {
				t.Fatalf("Allowed(%v, %v) = %v, want %v", tt.set, tt.peer, got, tt.want)
			}
		})
	}
}
