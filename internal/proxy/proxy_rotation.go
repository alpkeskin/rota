package proxy

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/exp/rand"
)

func (ps *ProxyServer) getProxy() *Proxy {
	method := ps.cfg.Proxy.Rotation.Method
	switch method {
	case "random":
		return ps.Proxies[rand.Intn(len(ps.Proxies))]
	case "roundrobin":
		proxy := ps.Proxies[0]
		ps.Proxies = append(ps.Proxies[1:], proxy)
		return proxy
	case "least_conn":
		minUsedProxy := ps.Proxies[0]
		minUsageCount := minUsedProxy.UsageCount

		for _, proxy := range ps.Proxies[1:] {
			if proxy.UsageCount < minUsageCount {
				minUsageCount = proxy.UsageCount
			}
		}

		minUsedProxies := make([]*Proxy, 0)
		for _, proxy := range ps.Proxies {
			if proxy.UsageCount == minUsageCount {
				minUsedProxies = append(minUsedProxies, proxy)
			}
		}

		return minUsedProxies[rand.Intn(len(minUsedProxies))]
	case "time_based":
		currentTime := time.Now().Unix()
		interval := int64(ps.cfg.Proxy.Rotation.TimeBased.Interval)
		index := (currentTime / interval) % int64(len(ps.Proxies))
		return ps.Proxies[index]
	}

	return nil
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
		duration := time.Since(reqInfo.startAt)
		if err == nil && response != nil {
			go ps.updateProxyUsage(proxy, reqInfo, duration, "success")
			slog.Info(msgReqRotationSuccess,
				"request_id", reqInfo.id,
				"proxy", proxy.Host,
				"url", reqInfo.url,
				"duration", fmt.Sprintf("%.2fs", duration.Seconds()),
			)
			return response, nil
		}
		go ps.updateProxyUsage(proxy, reqInfo, duration, "failed")
		slog.Error(msgReqRotationError,
			"error", err,
			"request_id", reqInfo.id,
			"proxy", proxy.Host,
			"url", reqInfo.url,
			"duration", fmt.Sprintf("%.2fs", duration.Seconds()),
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
