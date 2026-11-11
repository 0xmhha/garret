package netguard

import (
	"cmp"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

var (
	// ErrNoPrivateNetwork means no up, non-loopback interface has a private
	// IPv4 prefix, so the allowed network must be given with --allow.
	ErrNoPrivateNetwork = errors.New("netguard: no private IPv4 network found; use --allow")
	// ErrAmbiguousNetwork means several interfaces have private IPv4 prefixes,
	// so the allowed network must be chosen with --allow.
	ErrAmbiguousNetwork = errors.New("netguard: several private networks found; choose one with --allow")
	// ErrPublicNetwork means an allowed prefix is not entirely private.
	ErrPublicNetwork = errors.New("netguard: allowed network is not private")
)

// privateRanges are the only address ranges garret may be opened to:
// RFC 1918 for IPv4, unique local (fc00::/7) and link-local (fe80::/10) for
// IPv6.
var privateRanges = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
}

// Interface describes one network interface of the host, as listed by the
// caller.
type Interface struct {
	Name     string
	Up       bool
	Loopback bool
	Prefixes []netip.Prefix
}

// IsPrivate reports whether every address in p lies inside one private range.
// An IPv4-mapped IPv6 prefix is judged by its IPv4 form. An invalid prefix is
// not private.
func IsPrivate(p netip.Prefix) bool {
	p, ok := unmapPrefix(p)
	if !ok {
		return false
	}
	for _, r := range privateRanges {
		if r.Addr().BitLen() == p.Addr().BitLen() && p.Bits() >= r.Bits() && r.Contains(p.Addr()) {
			return true
		}
	}
	return false
}

// ResolveAllowed returns the networks garret accepts connections from, sorted.
// A non-empty explicit list is used as given, masked and without duplicates.
// Otherwise the single private IPv4 network of the up, non-loopback
// interfaces is used. It returns ErrNoPrivateNetwork or ErrAmbiguousNetwork
// when there is not exactly one, and ErrPublicNetwork when a resulting
// prefix is not private. The inputs are not modified.
func ResolveAllowed(explicit []netip.Prefix, ifaces []Interface) ([]netip.Prefix, error) {
	var allowed []netip.Prefix
	if len(explicit) > 0 {
		for _, p := range explicit {
			allowed = append(allowed, p.Masked())
		}
	} else {
		found, err := detect(ifaces)
		if err != nil {
			return nil, err
		}
		allowed = []netip.Prefix{found}
	}
	for _, p := range allowed {
		if !IsPrivate(p) {
			return nil, fmt.Errorf("%w: %s", ErrPublicNetwork, p)
		}
	}
	slices.SortFunc(allowed, comparePrefix)
	return slices.Compact(allowed), nil
}

// candidate is a private IPv4 network found on an interface.
type candidate struct {
	iface  string
	prefix netip.Prefix
}

func detect(ifaces []Interface) (netip.Prefix, error) {
	var found []candidate
	for _, ifc := range ifaces {
		if !ifc.Up || ifc.Loopback {
			continue
		}
		for _, p := range ifc.Prefixes {
			if p.Addr().Is4() && IsPrivate(p) {
				found = append(found, candidate{ifc.Name, p.Masked()})
			}
		}
	}
	switch len(found) {
	case 0:
		return netip.Prefix{}, ErrNoPrivateNetwork
	case 1:
		return found[0].prefix, nil
	}
	slices.SortFunc(found, func(a, b candidate) int {
		return cmp.Or(strings.Compare(a.iface, b.iface), comparePrefix(a.prefix, b.prefix))
	})
	names := make([]string, len(found))
	for i, c := range found {
		names[i] = c.iface + " " + c.prefix.String()
	}
	return netip.Prefix{}, fmt.Errorf("%w: %s", ErrAmbiguousNetwork, strings.Join(names, ", "))
}

// Allowed reports whether a connection from peer may proceed: peer is a
// loopback address or lies inside one of the prefixes in set. The caller
// passes the TCP peer address; request headers are never consulted. An
// invalid address is not allowed.
func Allowed(set []netip.Prefix, peer netip.Addr) bool {
	if !peer.IsValid() {
		return false
	}
	// Prefix.Contains never matches an address with a zone.
	peer = peer.Unmap().WithZone("")
	if peer.IsLoopback() {
		return true
	}
	for _, p := range set {
		if p.Contains(peer) {
			return true
		}
	}
	return false
}

func unmapPrefix(p netip.Prefix) (netip.Prefix, bool) {
	if !p.IsValid() {
		return netip.Prefix{}, false
	}
	a := p.Addr()
	if !a.Is4In6() {
		return p, true
	}
	const mappedBits = 96
	if p.Bits() < mappedBits {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(a.Unmap(), p.Bits()-mappedBits), true
}

func comparePrefix(a, b netip.Prefix) int {
	return cmp.Or(a.Addr().Compare(b.Addr()), cmp.Compare(a.Bits(), b.Bits()))
}
