package app

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/0xmhha/garret/internal/config"
)

func TestAliasOrigins(t *testing.T) {
	lan := netip.MustParseAddr("192.168.1.5")
	tests := []struct {
		name   string
		scheme string
		port   int
		cfg    config.Serve
		addrs  []netip.Addr
		want   []string
	}{
		{
			name: "host name and a local-network address", scheme: "https", port: 8443,
			cfg:   config.Serve{Host: "review.local"},
			addrs: []netip.Addr{lan, netip.MustParseAddr("127.0.0.1")},
			want:  []string{"https://localhost:8443", "https://127.0.0.1:8443", "https://[::1]:8443", "https://192.168.1.5:8443"},
		},
		{
			name: "host localhost is not repeated", scheme: "https", port: 8443,
			cfg:  config.Serve{Host: "localhost"},
			want: []string{"https://127.0.0.1:8443", "https://[::1]:8443"},
		},
		{
			name: "host is the address", scheme: "https", port: 8443,
			cfg:   config.Serve{Host: "192.168.1.5"},
			addrs: []netip.Addr{lan},
			want:  []string{"https://localhost:8443", "https://127.0.0.1:8443", "https://[::1]:8443"},
		},
		{
			name: "listen override over plain HTTP", scheme: "http", port: 9000,
			cfg:   config.Serve{Host: "review.local", Listen: netip.MustParseAddrPort("10.0.0.2:9000")},
			addrs: []netip.Addr{netip.MustParseAddr("fd00::5")},
			want: []string{"http://localhost:9000", "http://127.0.0.1:9000", "http://[::1]:9000",
				"http://[fd00::5]:9000", "http://10.0.0.2:9000"},
		},
		{
			name: "an address listed twice", scheme: "https", port: 8443,
			cfg:   config.Serve{Host: "review.local", Listen: netip.MustParseAddrPort("192.168.1.5:8443")},
			addrs: []netip.Addr{lan},
			want:  []string{"https://localhost:8443", "https://127.0.0.1:8443", "https://[::1]:8443", "https://192.168.1.5:8443"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aliasOrigins(tt.scheme, tt.port, tt.cfg, tt.addrs)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("aliasOrigins = %q, want %q", got, tt.want)
			}
		})
	}
}
