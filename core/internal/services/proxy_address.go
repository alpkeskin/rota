package services

import (
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// SkipReason identifies why a proxy address is excluded from GeoIP lookup.
type SkipReason string

const (
	// SkipUnparseable — the address contains no usable IP literal.
	SkipUnparseable SkipReason = "unparseable_address"
	// SkipReserved — the IP is private/loopback/reserved and ip-api cannot
	// (and should not) be asked about it.
	SkipReserved SkipReason = "reserved_or_private_ip"
)

// explicitReservedNetworks are reserved ranges that netip's IsPrivate and
// friends do not cover: 0.0.0.0/8 ("this network"), 192.0.0.0/16 (IETF
// protocol assignments), 100.64.0.0/10 (carrier-grade NAT), and
// 240.0.0.0/4 (reserved for future use — ip-api rejects it as "reserved
// range", so without this entry such proxies loop the external batch API
// forever).
var explicitReservedNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("192.0.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

// NormalizeProxyAddress reduces a raw proxy line to "host:port". It strips
// userinfo ("user:pass@"), protocol schemes ("socks5://"), and trailing
// labels ("ip:port:US"). Unrecognized input is returned trimmed as-is.
func NormalizeProxyAddress(address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return ""
	}
	if at := strings.LastIndex(address, "@"); at >= 0 {
		address = address[at+1:]
	}
	if idx := strings.Index(address, "://"); idx >= 0 {
		address = address[idx+3:]
	}

	if host, port, err := net.SplitHostPort(address); err == nil {
		return net.JoinHostPort(strings.Trim(host, "[]"), port)
	}

	// "ip:port:LABEL" — keep the leading ip:port when it is well-formed.
	parts := strings.Split(address, ":")
	if len(parts) >= 3 {
		if ip := net.ParseIP(parts[0]); ip != nil {
			if port, err := strconv.Atoi(parts[1]); err == nil && port >= 1 && port <= 65535 {
				return net.JoinHostPort(ip.String(), parts[1])
			}
		}
	}

	return address
}

// ExtractPublicIP parses the public IP out of a raw proxy address. The
// returned addr is Unmap()-ed (IPv4-in-IPv6 normalized to IPv4). The second
// value is a non-empty SkipReason when the address must not be looked up.
func ExtractPublicIP(address string) (netip.Addr, SkipReason) {
	norm := NormalizeProxyAddress(address)
	if norm == "" {
		return netip.Addr{}, SkipUnparseable
	}

	host := norm
	if h, _, err := net.SplitHostPort(norm); err == nil {
		host = h
	} else if _, err := netip.ParseAddr(norm); err != nil {
		// Not a bare IP literal — take the part before the first ':' as the
		// host (a bare IPv6 has no port and must not be cut at its colons).
		if i := strings.Index(norm, ":"); i > 0 {
			host = norm[:i]
		}
	}
	host = strings.Trim(host, "[]")

	parsed, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, SkipUnparseable
	}

	addr := parsed.Unmap()
	if reason := classifyAddr(addr); reason != "" {
		return addr, reason
	}
	return addr, ""
}

// classifyAddr reports the skip reason for a parsed IP: SkipReserved when it
// is private, loopback, link-local, multicast, unspecified, or in one of the
// explicit reserved ranges; the empty reason when it is a public address.
func classifyAddr(addr netip.Addr) SkipReason {
	addr = addr.Unmap()
	for _, prefix := range explicitReservedNetworks {
		if prefix.Contains(addr) {
			return SkipReserved
		}
	}
	switch {
	case addr.IsPrivate(),
		addr.IsLoopback(),
		addr.IsLinkLocalUnicast(),
		addr.IsMulticast(),
		addr.IsUnspecified():
		return SkipReserved
	}
	return ""
}
