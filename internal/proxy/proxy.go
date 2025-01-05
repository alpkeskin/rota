package proxy

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"errors"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/alpkeskin/rota/internal/middleware"
	"github.com/elazarl/goproxy"
	"github.com/google/uuid"
	"golang.org/x/exp/rand"
)

const (
	// HTTP Status Codes
	StatusProxyAuthRequired = 407
	StatusBadGateway        = 502

	msgFailedToListen         = "failed to listen"
	msgProxyServerStarted     = "rota proxy server started"
	msgRequestReceived        = "request received"
	msgAuthError              = "authentication error"
	msgReqRotationSuccess     = "request rotation success"
	msgReqRotationError       = "request rotation error"
	msgRemovingUnhealthyProxy = "removing unhealthy proxy"
	msgNoProxyFound           = "no proxy found"
	msgProxyAttemptsExhausted = "proxy attempts exhausted"
	msgAllProxyAttemptsFailed = "all proxy attempts failed"
	msgUnauthorized           = "Rota Proxy: Unauthorized. Request ID: %s"
	msgBadGateway             = "Rota Proxy: Bad Gateway. Request ID: %s"
)

var hopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Proxy-Connection",
	"Te", // canonicalized version of "TE"
	"Trailers",
	"Transfer-Encoding",
	"Upgrade",
}

type requestInfo struct {
	id      string
	url     string
	request *http.Request
	startAt time.Time
}

type Proxy struct {
	Scheme    string
	Host      string
	Url       *url.URL
	Transport *http.Transport
}

type ProxyServer struct {
	goProxy *goproxy.ProxyHttpServer
	Proxies []*Proxy
	cfg     *config.Config
}

func NewProxyServer(cfg *config.Config) *ProxyServer {
	return &ProxyServer{
		Proxies: make([]*Proxy, 0),
		cfg:     cfg,
		goProxy: goproxy.NewProxyHttpServer(),
	}
}

func (ps *ProxyServer) AddProxy(proxy *Proxy) {
	ps.Proxies = append(ps.Proxies, proxy)
}

func (ps *ProxyServer) getProxy() *Proxy {
	method := ps.cfg.Proxy.Rotation.Method

	switch method {
	case "random":
		return ps.Proxies[rand.Intn(len(ps.Proxies))]
	case "roundrobin":
		proxy := ps.Proxies[0]
		ps.Proxies = append(ps.Proxies[1:], proxy)
		return proxy
	}

	return nil
}

func (ps *ProxyServer) Listen() {
	ps.setUpHandlers()
	time.Sleep(500 * time.Millisecond)

	port := fmt.Sprintf(":%d", ps.cfg.Proxy.Port)
	slog.Info(msgProxyServerStarted, "port", port)
	err := http.ListenAndServe(port, ps.goProxy)
	if err != nil {
		slog.Error(msgFailedToListen, "error", err)
		return
	}
}

func (ps *ProxyServer) setUpHandlers() {
	ps.goProxy.OnRequest().HandleConnectFunc(ps.authenticateHttps)
	ps.goProxy.OnRequest().DoFunc(ps.handleRequest)
}

func (ps *ProxyServer) handleRequest(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	reqInfo := requestInfo{
		id:      uuid.New().String(),
		url:     r.URL.String(),
		request: r,
		startAt: time.Now(),
	}

	if r.URL.Scheme == "http" && ps.cfg.Proxy.Authentication.Enabled {
		if err := ps.authenticateHttp(ctx, reqInfo); err != nil {
			return ps.unauthorizedResponse(reqInfo)
		}
	}

	response, err := ps.tryProxies(reqInfo)
	if err != nil {
		return ps.badGatewayResponse(reqInfo, err)
	}

	return r, response
}

func (ps *ProxyServer) authenticateHttp(ctx *goproxy.ProxyCtx, reqInfo requestInfo) error {
	mid := middleware.NewMiddleware(ps.cfg)
	if err := mid.ProxyAuth(ctx); err != nil {
		slog.Error(msgAuthError, "error", err, "request_id", reqInfo.id, "url", reqInfo.url)
		return err
	}
	return nil
}

func (ps *ProxyServer) authenticateHttps(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
	if !ps.cfg.Proxy.Authentication.Enabled {
		return goproxy.MitmConnect, host
	}

	mid := middleware.NewMiddleware(ps.cfg)
	if err := mid.ProxyAuth(ctx); err != nil {
		slog.Error(msgAuthError, "error", err, "url", host)
		return goproxy.RejectConnect, host
	}
	return goproxy.MitmConnect, host
}

func (ps *ProxyServer) tryProxies(reqInfo requestInfo) (*http.Response, error) {
	for attempt := 0; attempt < ps.cfg.Proxy.Rotation.FallbackMaxRetries; attempt++ {
		proxy := ps.getProxy()
		if proxy == nil {
			slog.Error(msgNoProxyFound, "request_id", reqInfo.id, "url", reqInfo.url)
			return nil, errors.New(msgNoProxyFound)
		}

		if response, err := ps.tryProxy(proxy, reqInfo); err == nil {
			return response, nil
		}

		if ps.cfg.Proxy.Rotation.RemoveUnhealthy {
			slog.Warn(msgRemovingUnhealthyProxy, "request_id", reqInfo.id, "proxy", proxy.Host, "url", reqInfo.url)
			ps.removeUnhealthyProxy(proxy)
		}

		if !ps.cfg.Proxy.Rotation.Fallback {
			break
		}
	}
	return nil, errors.New(msgAllProxyAttemptsFailed)
}

func (ps *ProxyServer) tryProxy(proxy *Proxy, reqInfo requestInfo) (*http.Response, error) {
	for i := 0; i < ps.cfg.Proxy.Rotation.Retries; i++ {
		client := &http.Client{
			Transport: proxy.Transport,
			Timeout:   time.Duration(ps.cfg.Proxy.Rotation.Timeout) * time.Second,
		}
		defer client.CloseIdleConnections()

		ps.removeHopHeaders(reqInfo.request)
		reqInfo.request.RequestURI = ""
		response, err := client.Do(reqInfo.request)
		if err == nil && response != nil {
			duration := time.Since(reqInfo.startAt)
			slog.Info(msgReqRotationSuccess,
				"request_id", reqInfo.id,
				"proxy", proxy.Host,
				"url", reqInfo.url,
				"duration", fmt.Sprintf("%.2fs", duration.Seconds()),
			)
			return response, nil
		}
		slog.Error(msgReqRotationError,
			"error", err,
			"request_id", reqInfo.id,
			"proxy", proxy.Host,
			"url", reqInfo.url,
		)
	}
	return nil, errors.New(msgProxyAttemptsExhausted)
}

func (ps *ProxyServer) removeUnhealthyProxy(proxy *Proxy) {
	for i, p := range ps.Proxies {
		if p == proxy {
			ps.Proxies = append(ps.Proxies[:i], ps.Proxies[i+1:]...)
			return
		}
	}
}

func (ps *ProxyServer) removeHopHeaders(r *http.Request) {
	for _, h := range hopHeaders {
		r.Header.Del(h)
	}
}

func (ps *ProxyServer) unauthorizedResponse(reqInfo requestInfo) (*http.Request, *http.Response) {
	return nil, goproxy.NewResponse(reqInfo.request,
		goproxy.ContentTypeText, StatusProxyAuthRequired,
		fmt.Sprintf(msgUnauthorized, reqInfo.id))
}

func (ps *ProxyServer) badGatewayResponse(reqInfo requestInfo, err error) (*http.Request, *http.Response) {
	slog.Error(msgReqRotationError, "error", err, "request_id", reqInfo.id, "url", reqInfo.url)
	return nil, goproxy.NewResponse(reqInfo.request,
		goproxy.ContentTypeText, StatusBadGateway,
		fmt.Sprintf(msgBadGateway, reqInfo.id))
}
