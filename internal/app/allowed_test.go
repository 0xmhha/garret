package app

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/0xmhha/garret/internal/netguard"
)

// withBridge adds a virtual machine's network to the test system.
func withBridge(sys System) System {
	base := sys.Interfaces
	sys.Interfaces = func() ([]netguard.Interface, error) {
		ifaces, err := base()
		bridge := netguard.Interface{Name: "bridge100", Up: true, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.64.1/24")}}
		return append(ifaces, bridge), err
	}
	return sys
}

func TestNewUsesTheNetworkOfTheFirstStart(t *testing.T) {
	cfg := testConfig(t, false)
	_, _, stop := start(t, cfg)
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	a, err := New(context.Background(), cfg, withBridge(testSystem(t, &out)))
	if err != nil {
		t.Fatalf("second start with a new network: %v", err)
	}
	defer a.closeAll()
	if want := []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}; !slices.Equal(a.allowed, want) {
		t.Fatalf("allowed %v, want %v", a.allowed, want)
	}
	if strings.Contains(out.String(), "192.168.64.0/24") {
		t.Fatalf("banner names the virtual machine network:\n%s", out.String())
	}
}

func TestNewRefusesSeveralNetworksAtTheFirstStart(t *testing.T) {
	var out strings.Builder
	_, err := New(context.Background(), testConfig(t, false), withBridge(testSystem(t, &out)))
	var amb *netguard.AmbiguousError
	if !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Fatalf("err = %v, want an *AmbiguousError with both networks", err)
	}
}
