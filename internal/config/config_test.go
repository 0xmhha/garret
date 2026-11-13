package config

import (
	"errors"
	"net/netip"
	"slices"
	"testing"
)

var env = Environment{OS: "darwin", Home: "/Users/ann", Hostname: "Anns-MacBook"}

func TestDefaults(t *testing.T) {
	c, err := ParseServe(nil, env)
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != "/Users/ann/Library/Application Support/garret" {
		t.Errorf("data dir %q", c.DataDir)
	}
	if c.Host != "anns-macbook.local" {
		t.Errorf("host %q", c.Host)
	}
	if c.Port != DefaultPort || c.EnrollPort != DefaultEnrollPort || !c.ClientCerts || c.InsecureHTTP {
		t.Errorf("config %+v", c)
	}
}

func TestLinuxDataDir(t *testing.T) {
	c, _ := ParseServe(nil, Environment{OS: "linux", Home: "/home/ann", Hostname: "box"})
	if c.DataDir != "/home/ann/.local/share/garret" {
		t.Errorf("data dir %q", c.DataDir)
	}
	c, _ = ParseServe(nil, Environment{OS: "linux", Home: "/home/ann", XDGDataHome: "/data/xdg", Hostname: "box"})
	if c.DataDir != "/data/xdg/garret" {
		t.Errorf("XDG data dir %q", c.DataDir)
	}
}

func TestFlags(t *testing.T) {
	c, err := ParseServe([]string{"--data", "/srv/garret", "--host", "review.lan", "--allow", "192.168.1.0/24",
		"--allow", "10.0.0.0/8", "--listen", "192.168.1.5:9443", "--enroll-listen", "192.168.1.5:9444", "--client-cert=off"}, env)
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != "/srv/garret" || c.Host != "review.lan" || c.ClientCerts {
		t.Errorf("config %+v", c)
	}
	if !slices.Equal(c.Allow, []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24"), netip.MustParsePrefix("10.0.0.0/8")}) {
		t.Errorf("allow %v", c.Allow)
	}
	if c.Listen != netip.MustParseAddrPort("192.168.1.5:9443") || c.EnrollListen != netip.MustParseAddrPort("192.168.1.5:9444") {
		t.Errorf("listen %v %v", c.Listen, c.EnrollListen)
	}
}

func TestInsecureHTTPImpliesNoClientCerts(t *testing.T) {
	c, err := ParseServe([]string{"--insecure-http"}, env)
	if err != nil || c.ClientCerts || !c.InsecureHTTP {
		t.Fatalf("config %+v, %v", c, err)
	}
}

func TestInvalid(t *testing.T) {
	cases := map[string][]string{
		"bad allow":         {"--allow", "nope"},
		"public allow":      {"--allow", "8.8.8.0/24"},
		"bad listen":        {"--listen", "host-only"},
		"cert without key":  {"--tls-cert", "/x.pem"},
		"key without cert":  {"--tls-key", "/x.key"},
		"bad client-cert":   {"--client-cert=maybe"},
		"bad host":          {"--host", "bad host"},
		"relative data":     {"--data", "relative/dir"},
		"unknown flag":      {"--wat"},
		"extra argument":    {"stray"},
		"same ports":        {"--listen", "192.168.1.5:9443", "--enroll-listen", "192.168.1.5:9443"},
		"insecure with tls": {"--insecure-http", "--tls-cert", "/c.pem", "--tls-key", "/k.pem"},
	}
	for name, args := range cases {
		if _, err := ParseServe(args, env); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestDefaultHost(t *testing.T) {
	for in, want := range map[string]string{
		"Anns-MacBook":    "anns-macbook.local",
		"box.example.lan": "box.example.lan",
		"Box.Local":       "box.local",
		"":                "localhost",
		"bad_name!":       "localhost",
	} {
		if got := DefaultHost(in); got != want {
			t.Errorf("DefaultHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClientCertsSet(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"--client-cert=on"}, true},
		{[]string{"--client-cert=off"}, true},
		{[]string{"--insecure-http"}, true},
		{[]string{"--host", "review.lan"}, false},
	}
	for _, tt := range tests {
		c, err := ParseServe(tt.args, env)
		if err != nil || c.ClientCertsSet != tt.want {
			t.Errorf("%v: ClientCertsSet = %v, %v; want %v", tt.args, c.ClientCertsSet, err, tt.want)
		}
	}
}
