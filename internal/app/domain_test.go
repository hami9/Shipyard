package app

import (
	"net/netip"
	"strings"
	"testing"
)

func TestNormalizeHostname(t *testing.T) {
	for in, want := range map[string]string{
		"app.example.com":      "app.example.com",
		"App.Example.COM":      "app.example.com",
		"app.example.com.":     "app.example.com",
		"  a-b.c1.example.io ": "a-b.c1.example.io",
		"xn--mnchen-3ya.de":    "xn--mnchen-3ya.de",
	} {
		if got, err := NormalizeHostname(in); err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "localhost", "*.example.com", "1.2.3.4", "app..example.com", "-app.example.com",
		"app-.example.com", "app_x.example.com", "app.example.com/path", "app.example.com:443", "[::1]", "münchen.de",
		strings.Repeat(strings.Repeat("a", 60)+".", 5) + "com"} { // 308 characters
		if got, err := NormalizeHostname(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func TestSuffixAllowed(t *testing.T) {
	list := []string{"example.com", "apps.example.org"}
	for host, want := range map[string]bool{
		"example.com":          true,
		"a.example.com":        true,
		"b.a.example.com":      true,
		"x.apps.example.org":   true,
		"apps.example.org":     true,
		"example.org":          false,
		"notexample.com":       false, // a suffix match must be at a label boundary
		"example.com.evil.net": false,
	} {
		if got := SuffixAllowed(host, list); got != want {
			t.Errorf("%s = %v, want %v", host, got, want)
		}
	}
	if !SuffixAllowed("anything.net", nil) {
		t.Error("an empty list must allow any hostname")
	}
}

func TestPointsHere(t *testing.T) {
	ours := []netip.Addr{netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("2001:db8::10")}
	a := func(s ...string) (out []netip.Addr) {
		for _, x := range s {
			out = append(out, netip.MustParseAddr(x))
		}
		return
	}
	for name, tc := range map[string]struct {
		addrs   []netip.Addr
		ok      bool
		foreign int
	}{
		"A only":             {a("203.0.113.10"), true, 0},
		"A and AAAA":         {a("203.0.113.10", "2001:db8::10"), true, 0},
		"v4-mapped":          {a("::ffff:203.0.113.10"), true, 0},
		"no records":         {nil, false, 0},
		"elsewhere":          {a("198.51.100.7"), false, 1},
		"one stray AAAA":     {a("203.0.113.10", "2001:db8::99"), false, 1},
		"all foreign, mixed": {a("198.51.100.7", "2001:db8::99"), false, 2},
	} {
		ok, foreign := PointsHere(tc.addrs, ours)
		if ok != tc.ok || len(foreign) != tc.foreign {
			t.Errorf("%s: ok=%v foreign=%v", name, ok, foreign)
		}
	}
}

func TestUpstream(t *testing.T) {
	if got := Upstream("web", "11111111-1111-4111-8111-111111111111", 3000); got != "shipyard-web-11111111-1111-4111-8111-111111111111:3000" {
		t.Fatalf("Upstream = %s", got)
	}
}
