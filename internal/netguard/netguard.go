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

// AmbiguousError is returned by ResolveAllowed when several private
// networks are on the host and none of them is known. It matches
// ErrAmbiguousNetwork with errors.Is.
type AmbiguousError struct {
	// Candidates are sorted by interface name, then prefix.
	Candidates []Candidate
}

// Candidate is a private IPv4 network found on an interface.
type Candidate struct {
	Interface string
	Prefix    netip.Prefix
}

func (e *AmbiguousError) Error() string {
	names := make([]string, len(e.Candidates))
	for i, c := range e.Candidates {
		names[i] = c.Interface + " " + c.Prefix.String()
	}
	return ErrAmbiguousNetwork.Error() + ": " + strings.Join(names, ", ")
}

// Is reports whether target is ErrAmbiguousNetwork.
func (e *AmbiguousError) Is(target error) bool { return target == ErrAmbiguousNetwork }

// ResolveAllowed returns the allowed networks, sorted and without
// duplicates. A non-empty explicit list is used as given, masked.
// Otherwise the private IPv4 network of the up, non-loopback interfaces is
// used; when there are several, those inside a prefix of known (the
// networks of the stored CA, decisions.md D19) are used. It returns
// ErrNoPrivateNetwork when there is none, an *AmbiguousError when there
// are several and known settles nothing, and ErrPublicNetwork when a
// resulting prefix is not private. The inputs are not modified.
func ResolveAllowed(explicit, known []netip.Prefix, ifaces []Interface) ([]netip.Prefix, error) {
	var allowed []netip.Prefix
	if len(explicit) > 0 {
		for _, p := range explicit {
			allowed = append(allowed, p.Masked())
		}
	} else {
		found, err := detect(ifaces, known)
		if err != nil {
			return nil, err
		}
		allowed = found
	}
	for _, p := range allowed {
		if !IsPrivate(p) {
			return nil, fmt.Errorf("%w: %s", ErrPublicNetwork, p)
		}
	}
	slices.SortFunc(allowed, comparePrefix)
	return slices.Compact(allowed), nil
}

func detect(ifaces []Interface, known []netip.Prefix) ([]netip.Prefix, error) {
	var found []Candidate
	for _, ifc := range ifaces {
		if !ifc.Up || ifc.Loopback {
			continue
		}
		for _, p := range ifc.Prefixes {
			if p.Addr().Is4() && IsPrivate(p) {
				found = append(found, Candidate{ifc.Name, p.Masked()})
			}
		}
	}
	switch len(found) {
	case 0:
		return nil, ErrNoPrivateNetwork
	case 1:
		return []netip.Prefix{found[0].Prefix}, nil
	}
	var chosen []netip.Prefix
	for _, c := range found {
		if inside(c.Prefix, known) {
			chosen = append(chosen, c.Prefix)
		}
	}
	if len(chosen) > 0 {
		return chosen, nil
	}
	slices.SortFunc(found, func(a, b Candidate) int {
		return cmp.Or(strings.Compare(a.Interface, b.Interface), comparePrefix(a.Prefix, b.Prefix))
	})
	return nil, &AmbiguousError{Candidates: found}
}

// inside reports whether p lies entirely within one prefix of set of the
// same address family.
func inside(p netip.Prefix, set []netip.Prefix) bool {
	return slices.ContainsFunc(set, func(r netip.Prefix) bool {
		return r.Addr().BitLen() == p.Addr().BitLen() && p.Bits() >= r.Bits() && r.Contains(p.Addr())
	})
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
