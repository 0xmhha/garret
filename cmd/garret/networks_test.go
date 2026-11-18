package main

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/0xmhha/garret/internal/netguard"
)

func TestPrintNetworkChoice(t *testing.T) {
	amb := &netguard.AmbiguousError{Candidates: []netguard.Candidate{
		{Interface: "bridge100", Prefix: netip.MustParsePrefix("192.168.64.0/24")},
		{Interface: "en1", Prefix: netip.MustParsePrefix("10.20.0.0/16")},
	}}
	var b strings.Builder
	printNetworkChoice(&b, amb)
	got := b.String()
	for _, want := range []string{
		"several private networks",
		"  bridge100  garret serve --allow 192.168.64.0/24\n",
		"  en1        garret serve --allow 10.20.0.0/16\n",
		"Virtual machines",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("guide lacks %q:\n%s", want, got)
		}
	}
}
