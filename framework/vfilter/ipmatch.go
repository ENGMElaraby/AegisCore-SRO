package vfilter

import "net/netip"

// Contains reports whether ip is entry (a single address) or inside entry (a CIDR range in canonical form, as the
// panel stores filter_lists values; contract 0.3.2). IPv4-mapped IPv6 addresses are compared as IPv4.
func Contains(entry string, ip netip.Addr) bool {
	ip = ip.Unmap()
	if prefix, err := netip.ParsePrefix(entry); err == nil {
		return prefix.Masked().Contains(ip)
	}
	if addr, err := netip.ParseAddr(entry); err == nil {
		return addr.Unmap() == ip
	}
	return false
}

// AnyContains reports whether any entry matches ip.
func AnyContains(entries []string, ip netip.Addr) bool {
	for _, entry := range entries {
		if Contains(entry, ip) {
			return true
		}
	}
	return false
}
