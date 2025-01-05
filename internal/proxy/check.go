package proxy

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/gammazero/workerpool"
)

const (
	msgFailedToLoadProxies       = "failed to load proxies"
	msgLoadingProxies            = "loading proxies"
	msgProxiesLoadedSuccessfully = "proxies loaded successfully"
	msgUnsupportedProxyScheme    = "unsupported proxy scheme"
	msgCheckingProxies           = "checking proxies"
	msgFailedToCreateOutputFile  = "failed to create output file"
	msgFailedToCreateRequest     = "failed to create request"
	msgDeadProxy                 = "dead proxy"
	msgAliveProxy                = "alive proxy"
	msgFailedToWriteOutputFile   = "failed to write output file"
)

type ProxyChecker struct {
	cfg         *config.Config
	proxyServer *ProxyServer
}

func NewProxyChecker(cfg *config.Config, proxyServer *ProxyServer) *ProxyChecker {
	return &ProxyChecker{
		cfg:         cfg,
		proxyServer: proxyServer,
	}
}

func (pl *ProxyChecker) Check() error {
	var outputFile *os.File
	var err error

	if pl.cfg.Healthcheck.Output.Method == "file" {
		outputFile, err = os.Create(pl.cfg.Healthcheck.Output.File)
		if err != nil {
			return fmt.Errorf("%s: %w", msgFailedToCreateOutputFile, err)
		}
		defer outputFile.Close()
	}

	wp := workerpool.New(pl.cfg.Healthcheck.Workers)

	for _, proxy := range pl.proxyServer.Proxies {
		wp.Submit(func() {
			pl.checkProxy(proxy, outputFile)
		})
	}

	wp.StopWait()
	return nil
}

func (pl *ProxyChecker) checkProxy(proxy *Proxy, outputFile *os.File) {
	client := &http.Client{
		Transport: proxy.Transport,
		Timeout:   time.Duration(pl.cfg.Healthcheck.Timeout) * time.Second,
	}

	req, err := http.NewRequest("GET", pl.cfg.Healthcheck.URL, nil)
	if err != nil {
		slog.Error(msgDeadProxy, "error", err, "proxy", proxy.Host)
		return
	}

	for _, header := range pl.cfg.Healthcheck.Headers {
		parts := strings.SplitN(header, ":", 2)
		if len(parts) == 2 {
			req.Header.Add(parts[0], parts[1])
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		slog.Error(msgDeadProxy, "error", err, "proxy", proxy.Host)
		return
	}

	if resp.StatusCode != pl.cfg.Healthcheck.Status {
		slog.Error(msgDeadProxy, "error", fmt.Errorf("status code: %d", resp.StatusCode), "proxy", proxy.Host)
		return
	}

	slog.Info(msgAliveProxy, "proxy", proxy.Host)
	if outputFile != nil {
		_, err = outputFile.WriteString(proxy.Host + "\n")
		if err != nil {
			slog.Error(msgFailedToWriteOutputFile, "error", err)
		}
	}
}
