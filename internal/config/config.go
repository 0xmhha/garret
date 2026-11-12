package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/0xmhha/garret/internal/netguard"
	"github.com/0xmhha/garret/internal/pki"
)

// ErrInvalid is returned for any invalid flag value.
var ErrInvalid = errors.New("config: invalid configuration")

// Default ports (transport.md, T-D2).
const (
	DefaultPort       = 8443
	DefaultEnrollPort = 8444
	appDir            = "garret"
	localSuffix       = ".local"
	clientCertsOn     = "on"
	clientCertsOff    = "off"
)

// Environment is what defaults are computed from. The caller fills it from
// the process environment, so parsing itself reads nothing.
type Environment struct {
	OS          string // runtime.GOOS
	Home        string
	XDGDataHome string
	Hostname    string
}

// Serve is the validated configuration of garret serve. A zero Listen or
// EnrollListen means: the allowed networks' interface addresses on Port or
// EnrollPort.
type Serve struct {
	DataDir      string
	Host         string
	Allow        []netip.Prefix
	Listen       netip.AddrPort
	EnrollListen netip.AddrPort
	Port         uint16
	EnrollPort   uint16
	ClientCerts  bool
	TLSCert      string
	TLSKey       string
	InsecureHTTP bool
}

// ParseServe parses the flags of garret serve and validates them.
func ParseServe(args []string, env Environment) (Serve, error) {
	c := Serve{Port: DefaultPort, EnrollPort: DefaultEnrollPort}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&c.DataDir, "data", DefaultDataDir(env), "data directory")
	fs.StringVar(&c.Host, "host", DefaultHost(env.Hostname), "host name in the server certificate")
	var listen, enrollListen, clientCerts string
	var allows []string
	fs.Func("allow", "allowed network in CIDR form (repeatable)", func(v string) error {
		allows = append(allows, v)
		return nil
	})
	fs.StringVar(&listen, "listen", "", "address:port of the main listener")
	fs.StringVar(&enrollListen, "enroll-listen", "", "address:port of the enrollment listener")
	fs.StringVar(&clientCerts, "client-cert", clientCertsOn, "require client certificates: on or off")
	fs.StringVar(&c.TLSCert, "tls-cert", "", "externally issued server certificate (PEM)")
	fs.StringVar(&c.TLSKey, "tls-key", "", "key of --tls-cert (PEM)")
	fs.BoolVar(&c.InsecureHTTP, "insecure-http", false, "serve plain HTTP (implies --client-cert=off)")
	if err := fs.Parse(args); err != nil {
		return Serve{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if fs.NArg() > 0 {
		return Serve{}, fmt.Errorf("%w: unexpected argument %q", ErrInvalid, fs.Arg(0))
	}
	for _, a := range allows {
		p, err := netip.ParsePrefix(a)
		if err != nil || !netguard.IsPrivate(p.Masked()) {
			return Serve{}, fmt.Errorf("%w: --allow %q is not a private network in CIDR form", ErrInvalid, a)
		}
		c.Allow = append(c.Allow, p.Masked())
	}
	var err error
	if c.Listen, err = parseAddrPort("listen", listen); err != nil {
		return Serve{}, err
	}
	if c.EnrollListen, err = parseAddrPort("enroll-listen", enrollListen); err != nil {
		return Serve{}, err
	}
	if c.Listen.IsValid() && c.Listen == c.EnrollListen {
		return Serve{}, fmt.Errorf("%w: --listen and --enroll-listen must differ", ErrInvalid)
	}
	switch clientCerts {
	case clientCertsOn:
		c.ClientCerts = true
	case clientCertsOff:
	default:
		return Serve{}, fmt.Errorf("%w: --client-cert must be on or off", ErrInvalid)
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return Serve{}, fmt.Errorf("%w: --tls-cert and --tls-key go together", ErrInvalid)
	}
	if c.InsecureHTTP {
		if c.TLSCert != "" {
			return Serve{}, fmt.Errorf("%w: --insecure-http cannot be combined with --tls-cert", ErrInvalid)
		}
		c.ClientCerts = false
	}
	if !pki.ValidHost(c.Host) {
		return Serve{}, fmt.Errorf("%w: --host %q is not a host name", ErrInvalid, c.Host)
	}
	if !filepath.IsAbs(c.DataDir) {
		return Serve{}, fmt.Errorf("%w: --data must be an absolute path", ErrInvalid)
	}
	return c, nil
}

func parseAddrPort(name, v string) (netip.AddrPort, error) {
	if v == "" {
		return netip.AddrPort{}, nil
	}
	ap, err := netip.ParseAddrPort(v)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("%w: --%s %q must be address:port", ErrInvalid, name, v)
	}
	return ap, nil
}

// DefaultDataDir is the per-user application data directory
// (components.md, Q1).
func DefaultDataDir(env Environment) string {
	if env.OS == "darwin" {
		return filepath.Join(env.Home, "Library", "Application Support", appDir)
	}
	if env.XDGDataHome != "" {
		return filepath.Join(env.XDGDataHome, appDir)
	}
	return filepath.Join(env.Home, ".local", "share", appDir)
}

// DefaultHost turns the computer name into the certificate host name: in
// lowercase, with ".local" added to a single-label name (transport.md, Q6).
// A name that cannot be a host name gives "localhost".
func DefaultHost(hostname string) string {
	h := strings.ToLower(strings.TrimSuffix(hostname, "."))
	if h == "" || strings.ContainsFunc(h, func(r rune) bool { return r > unicode.MaxASCII }) {
		return "localhost"
	}
	if !strings.Contains(h, ".") {
		h += localSuffix
	}
	if !pki.ValidHost(h) {
		return "localhost"
	}
	return h
}
