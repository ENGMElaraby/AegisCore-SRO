package vfilter

import (
	"net/netip"
	"testing"
)

func TestContains(t *testing.T) {
	cases := []struct {
		entry string
		ip    string
		want  bool
	}{
		{"1.2.3.4", "1.2.3.4", true},
		{"1.2.3.4", "1.2.3.5", false},
		{"10.0.0.0/8", "10.200.1.1", true},
		{"10.0.0.0/8", "11.0.0.1", false},
		{"127.0.0.0/8", "::ffff:127.0.0.1", true},
		{"2001:db8::/32", "2001:db8::1", true},
		{"2001:db8::/32", "2001:db9::1", false},
		{"garbage", "1.2.3.4", false},
	}
	for _, c := range cases {
		if got := Contains(c.entry, netip.MustParseAddr(c.ip)); got != c.want {
			t.Errorf("Contains(%q, %s) = %v, want %v", c.entry, c.ip, got, c.want)
		}
	}
}
