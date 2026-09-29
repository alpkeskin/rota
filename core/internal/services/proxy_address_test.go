package services

import (
	"net/netip"
	"testing"
)

func TestNormalizeProxyAddress(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"bare ip", "1.2.3.4", "1.2.3.4"},
		{"ip:port", "1.2.3.4:8080", "1.2.3.4:8080"},
		{"userinfo", "user:pass@1.2.3.4:8080", "1.2.3.4:8080"},
		{"trailing label", "1.2.3.4:8080:US", "1.2.3.4:8080"},
		{"userinfo and label", "user:pass@1.2.3.4:8080:US", "1.2.3.4:8080"},
		{"scheme", "socks5://1.2.3.4:8080", "1.2.3.4:8080"},
		{"scheme and userinfo", "http://user:pass@1.2.3.4:8080", "1.2.3.4:8080"},
		{"bracketed ipv6 (canonical form kept)", "[2001:db8::1]:443", "[2001:db8::1]:443"},
		{"whitespace", "  1.2.3.4:8080  ", "1.2.3.4:8080"},
		{"empty", "", ""},
		{"hostname kept", "proxy.example.com:3128", "proxy.example.com:3128"},
		{"port out of range kept as-is", "1.2.3.4:99999:US", "1.2.3.4:99999:US"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeProxyAddress(tt.input); got != tt.want {
				t.Fatalf("NormalizeProxyAddress(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractPublicIP(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantIP  string // empty means "no addr"
		wantRsn SkipReason
	}{
		{"bare ip", "8.8.8.8", "8.8.8.8", ""},
		{"ip:port", "8.8.8.8:8080", "8.8.8.8", ""},
		{"userinfo", "user:pass@1.2.3.4:8080", "1.2.3.4", ""},
		{"trailing label", "1.2.3.4:8080:US", "1.2.3.4", ""},
		{"ipv6", "[2001:db8::1]:443", "2001:db8::1", ""},
		{"bare ipv6", "2001:db8::1", "2001:db8::1", ""},
		{"ipv4 in ipv6", "::ffff:1.2.3.4", "1.2.3.4", ""},
		{"ipv4 in ipv6 with port", "[::ffff:8.8.4.4]:3128", "8.8.4.4", ""},

		{"private 10/8", "10.1.2.3:8080", "10.1.2.3", SkipReserved},
		{"private 172.16/12", "172.16.5.4:8080", "172.16.5.4", SkipReserved},
		{"private 192.168/16", "192.168.1.1:8080", "192.168.1.1", SkipReserved},
		{"loopback", "127.0.0.1:22", "127.0.0.1", SkipReserved},
		{"unspecified", "0.0.0.0:80", "0.0.0.0", SkipReserved},
		{"this network 0/8", "0.1.2.3:80", "0.1.2.3", SkipReserved},
		{"ietf 192.0.0/16", "192.0.2.53:80", "192.0.2.53", SkipReserved},
		{"cgnat 100.64/10", "100.64.0.1:80", "100.64.0.1", SkipReserved},
		{"reserved 240/4 (241.x)", "241.35.84.94:80", "241.35.84.94", SkipReserved},
		{"reserved 240/4 (252.x)", "252.212.98.63:80", "252.212.98.63", SkipReserved},
		{"reserved 240/4 (255.x)", "255.74.1.47:80", "255.74.1.47", SkipReserved},
		{"link-local 169.254/16", "169.254.10.10:80", "169.254.10.10", SkipReserved},
		{"multicast", "224.0.0.1:80", "224.0.0.1", SkipReserved},
		{"ipv6 loopback", "[::1]:80", "::1", SkipReserved},
		{"ipv6 ula", "[fd00::1]:80", "fd00::1", SkipReserved},
		{"ipv6 link-local", "[fe80::1]:80", "fe80::1", SkipReserved},

		{"hostname", "proxy.example.com:3128", "", SkipUnparseable},
		{"garbage", "not-an-address", "", SkipUnparseable},
		{"empty", "", "", SkipUnparseable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := ExtractPublicIP(tt.input)
			if reason != tt.wantRsn {
				t.Fatalf("reason = %q, want %q", reason, tt.wantRsn)
			}
			if tt.wantIP == "" {
				if got.IsValid() {
					t.Fatalf("addr = %v, want invalid", got)
				}
				return
			}
			want := netip.MustParseAddr(tt.wantIP)
			if got != want {
				t.Fatalf("addr = %v, want %v", got, want)
			}
		})
	}
}
