package proxy

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/alpkeskin/rota/internal/config"
	"h12.io/socks"
)

const (
	msgFailedToCreateProxy       = "failed to create proxy"
	msgProxiesLoadedSuccessfully = "proxies loaded successfully"
	msgLoadingProxies            = "loading proxies"
	msgFailedToLoadProxies       = "failed to load proxies"
	msgUnsupportedProxyScheme    = "unsupported proxy scheme"
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

	p := Proxy{
		Scheme: parsedUrl.Scheme,
		Host:   proxyURL,
		Url:    parsedUrl,
	}

	tr := &http.Transport{}
	switch p.Scheme {
	case "socks4", "socks4a", "socks5":
		tr = &http.Transport{
			Dial: socks.Dial(p.Host),
		}
	case "http", "https":
		tr = &http.Transport{
			Proxy: http.ProxyURL(p.Url),
		}
	default:
		return nil, fmt.Errorf("%s. URL: %s", msgUnsupportedProxyScheme, proxyURL)
	}

	tr.DisableKeepAlives = true
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	p.Transport = tr
	return &p, nil
}
