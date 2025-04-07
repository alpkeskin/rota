package proxy

import (
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/alpkeskin/rota/internal/middleware"
	"github.com/elazarl/goproxy"
)

type ProxyServer struct {
	sync.RWMutex
	middleware   *middleware.Middleware
	goProxy      *goproxy.ProxyHttpServer
	Proxies      []*Proxy
	ProxyHistory []ProxyHistory
	cfg          *config.Config
}

func NewProxyServer(cfg *config.Config) *ProxyServer {
	return &ProxyServer{
		Proxies:      make([]*Proxy, 0),
		ProxyHistory: make([]ProxyHistory, 0, 1000),
		cfg:          cfg,
		goProxy:      goproxy.NewProxyHttpServer(),
		middleware:   middleware.NewMiddleware(cfg),
	}
}

func (ps *ProxyServer) AddProxy(proxy *Proxy) {
	ps.Proxies = append(ps.Proxies, proxy)
}

func (ps *ProxyServer) Listen() {
	ps.goProxy.CertStore = NewCertStorage()
	ps.setUpHandlers()
	time.Sleep(500 * time.Millisecond)

	host := ps.cfg.Proxy.Host
	port := ps.cfg.Proxy.Port
	address := fmt.Sprintf("%s:%d", host, port)

	slog.Info(msgProxyServerStarted, "address", address)
	err := http.ListenAndServe(address, ps.goProxy)
	if err != nil {
		slog.Error(msgFailedToListen, "error", err)
		return
	}
}

func (ps *ProxyServer) setUpHandlers() {
	ps.goProxy.OnRequest().HandleConnectFunc(ps.authenticateHttps)
	if ps.cfg.Proxy.RateLimit.Enabled {
		ps.goProxy.OnRequest().DoFunc(ps.rateLimitMiddleware)
	}
	ps.goProxy.OnRequest().DoFunc(ps.handleRequest)
}
