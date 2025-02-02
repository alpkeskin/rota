package proxy

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/google/uuid"
)

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

func (ps *ProxyServer) rateLimitMiddleware(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	if err := ps.middleware.RateLimit(r); err != nil {
		return nil, goproxy.NewResponse(r,
			goproxy.ContentTypeText, http.StatusTooManyRequests,
			fmt.Sprintf(msgRateLimitExceeded, r.RemoteAddr))
	}
	return r, nil
}

func (ps *ProxyServer) authenticateHttp(ctx *goproxy.ProxyCtx, reqInfo requestInfo) error {
	if err := ps.middleware.ProxyAuth(ctx); err != nil {
		slog.Error(msgAuthError, "error", err, "request_id", reqInfo.id, "url", reqInfo.url)
		return err
	}
	return nil
}

func (ps *ProxyServer) authenticateHttps(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
	if !ps.cfg.Proxy.Authentication.Enabled {
		return goproxy.MitmConnect, host
	}

	if err := ps.middleware.ProxyAuth(ctx); err != nil {
		slog.Error(msgAuthError, "error", err, "url", host)
		return goproxy.RejectConnect, host
	}
	return goproxy.MitmConnect, host
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
