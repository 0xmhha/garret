package main

import (
	"fmt"
	"io"

	"github.com/0xmhha/garret/internal/netguard"
)

// printNetworkChoice tells the operator how to choose among the private
// networks of amb, one ready command per network (decisions.md D19). It is
// written for the terminal, not for the log.
func printNetworkChoice(w io.Writer, amb *netguard.AmbiguousError) {
	width := 0
	for _, c := range amb.Candidates {
		width = max(width, len(c.Interface))
	}
	_, _ = fmt.Fprintln(w, "garret serve: this machine is on several private networks, and garret does not guess which one the team shares.")
	_, _ = fmt.Fprintln(w, "Start again with the network of the Wi-Fi or Ethernet connection the team is on:")
	_, _ = fmt.Fprintln(w)
	for _, c := range amb.Candidates {
		_, _ = fmt.Fprintf(w, "  %-*s  garret serve --allow %s\n", width, c.Interface, c.Prefix)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Virtual machines, containers and Internet Sharing add networks of their own.")
	_, _ = fmt.Fprintln(w, "garret remembers the network of its first start and picks it again by itself while the machine is on it.")
}
