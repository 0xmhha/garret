package netguard

import (
	"fmt"
	"log/slog"
	"net"
	"net/netip"
)

// Interfaces lists the host's network interfaces with their prefixes.
func Interfaces() ([]Interface, error) {
	list, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("netguard: list interfaces: %w", err)
	}
	out := make([]Interface, 0, len(list))
	for _, ifc := range list {
		addrs, err := ifc.Addrs()
		if err != nil {
			return nil, fmt.Errorf("netguard: addresses of %s: %w", ifc.Name, err)
		}
		i := Interface{
			Name:     ifc.Name,
			Up:       ifc.Flags&net.FlagUp != 0,
			Loopback: ifc.Flags&net.FlagLoopback != 0,
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			addr, ok := netip.AddrFromSlice(ipNet.IP)
			if !ok {
				continue
			}
			bits, _ := ipNet.Mask.Size()
			addr = addr.Unmap()
			if addr.Is4() && bits > 32 {
				bits -= 96
			}
			i.Prefixes = append(i.Prefixes, netip.PrefixFrom(addr, bits))
		}
		out = append(out, i)
	}
	return out, nil
}

// Listener wraps inner so that Accept returns only connections whose TCP
// peer is allowed by Allowed(allowed, peer). Other connections are closed at
// once and logged with their address, before any TLS or HTTP processing.
func Listener(inner net.Listener, allowed []netip.Prefix, log *slog.Logger) net.Listener {
	return &filterListener{Listener: inner, allowed: allowed, log: log}
}

type filterListener struct {
	net.Listener
	allowed []netip.Prefix
	log     *slog.Logger
}

func (l *filterListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if tcp, ok := c.RemoteAddr().(*net.TCPAddr); ok && Allowed(l.allowed, tcp.AddrPort().Addr()) {
			return c, nil
		}
		l.log.Warn("connection rejected: outside the allowed network", "remote", c.RemoteAddr().String())
		_ = c.Close()
	}
}
