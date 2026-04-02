package services

import (
	"context"
	"fmt"
	"net"
	"net/url"

	"h12.io/socks"
	proxyDialer "golang.org/x/net/proxy"
)

// parseURL wraps url.Parse with a helpful error
func parseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	return u, nil
}

// buildSocksDialer returns a DialContext-compatible function for SOCKS proxies
func buildSocksDialer(address, protocol string) (func(context.Context, string, string) (net.Conn, error), error) {
	proxyURL := fmt.Sprintf("%s://%s", protocol, address)
	switch protocol {
	case "socks4", "socks4a":
		dialFn := socks.Dial(proxyURL)
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialFn(network, addr)
		}, nil
	case "socks5":
		dialer, err := proxyDialer.SOCKS5("tcp", address, nil, proxyDialer.Direct)
		if err != nil {
			return nil, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
		}
		dc, ok := dialer.(interface {
			DialContext(ctx context.Context, network, addr string) (net.Conn, error)
		})
		if ok {
			return dc.DialContext, nil
		}
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		}, nil
	}
	return nil, fmt.Errorf("unsupported socks protocol: %s", protocol)
}
