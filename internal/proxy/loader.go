package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/alpkeskin/rota/internal/config"
	"h12.io/socks"
)

type ProxyLoader struct {
	cfg         *config.Config
	proxyServer *ProxyServer
}

func NewProxyLoader(cfg *config.Config, proxyServer *ProxyServer) *ProxyLoader {
	return &ProxyLoader{
		cfg:         cfg,
		proxyServer: proxyServer,
	}
}

func (pl *ProxyLoader) Load() error {
	slog.Info(msgLoadingProxies)
	data, err := os.ReadFile(pl.cfg.ProxyFile)
	if err != nil {
		return fmt.Errorf("%s: %w", msgFailedToLoadProxies, err)
	}

	content := strings.TrimSpace(string(data))
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		proxy, err := pl.CreateProxy(line)
		if err != nil {
			slog.Error(msgFailedToCreateProxy, "error", err, "proxy", line)
			continue
		}

		pl.proxyServer.AddProxy(proxy)
	}

	slog.Info(msgProxiesLoadedSuccessfully)
	return nil
}

func (pl *ProxyLoader) Reload() error {
	pl.proxyServer.Proxies = make([]*Proxy, 0)
	return pl.Load()
}

func (pl *ProxyLoader) CreateProxy(proxyURL string) (*Proxy, error) {
	parsedUrl, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}

	var username, password string
	if parsedUrl.User != nil {
		username = parsedUrl.User.Username()
		password, _ = parsedUrl.User.Password()
	}

	p := Proxy{
		Scheme:   parsedUrl.Scheme,
		Host:     proxyURL,
		Url:      parsedUrl,
		Username: username,
		Password: password,
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
		MaxVersion:         tls.VersionTLS13,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		},
	}

	tr := &http.Transport{
		TLSClientConfig:     tlsConfig,
		DisableKeepAlives:   true,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	switch p.Scheme {
	case "socks4", "socks4a", "socks5":
		proxyAddrURL := &url.URL{
			Host:   parsedUrl.Host,
			Scheme: parsedUrl.Scheme,
			User:   url.UserPassword(username, password),
		}
		dialSocks := socks.Dial(proxyAddrURL.String())
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialSocks(network, addr)
		}
	case "http", "https":
		tr.Proxy = http.ProxyURL(p.Url)
	default:
		return nil, fmt.Errorf("%s. URL: %s", msgUnsupportedProxyScheme, proxyURL)
	}

	p.Transport = tr
	return &p, nil
}
